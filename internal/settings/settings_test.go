package settings_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kiry163/llmmeter/internal/pricing"
	"github.com/kiry163/llmmeter/internal/settings"
	"github.com/kiry163/llmmeter/internal/store"
	"github.com/kiry163/llmmeter/internal/usage"
)

func newTestRepo(t *testing.T) *settings.Repo {
	t.Helper()
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	repo, err := settings.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream: settings.Upstream{
			BaseURL: "https://api.example.com",
			APIKey:  "sk-up",
		},
		Timezone: "UTC",
	}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestSetupAndSnapshot(t *testing.T) {
	db, err := store.OpenDB(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo, err := settings.Open(db)
	if err != nil {
		t.Fatal(err)
	}
	if repo.Snapshot().SetupDone {
		t.Fatal("new db must need setup")
	}

	key, err := repo.Setup(context.Background(), settings.SetupInput{
		Upstream:      settings.Upstream{BaseURL: "https://api.example.com", APIKey: "sk-up"},
		Timezone:      "Asia/Shanghai",
		AdminPassword: "hunter22",
	})
	if err != nil {
		t.Fatal(err)
	}
	snap := repo.Snapshot()
	if !snap.SetupDone || snap.GatewayKey != key || len(key) != len("sk-local-")+32 {
		t.Fatalf("bad snapshot %+v key=%q", snap, key)
	}
	if snap.Location.String() != "Asia/Shanghai" || snap.RetentionDays != 90 {
		t.Fatalf("loc/retention = %v/%d", snap.Location, snap.RetentionDays)
	}
	if !settings.VerifyPassword(snap.AdminPasswordHash, "hunter22") {
		t.Fatal("password verify failed")
	}
	if len(snap.SessionSecret) != 32 {
		t.Fatalf("session secret len = %d", len(snap.SessionSecret))
	}
	if _, err := repo.Setup(context.Background(), settings.SetupInput{}); !errors.Is(err, settings.ErrAlreadySetup) {
		t.Fatalf("second setup err = %v", err)
	}
}

func TestUpdateRejectsBadTimezone(t *testing.T) {
	repo := newTestRepo(t)
	tz := "Mars/Olympus"
	err := repo.Update(context.Background(), settings.Patch{Timezone: &tz})
	var ve *settings.ValidationError
	if !errors.As(err, &ve) || ve.Fields["timezone"] == "" {
		t.Fatalf("err = %v", err)
	}
}

func TestModelCRUDRefreshesTable(t *testing.T) {
	repo := newTestRepo(t)
	m, err := repo.CreateModel(context.Background(), settings.ModelInput{
		Name: "deepseek-chat", Input: 0.28, Output: 0.42,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateModel(context.Background(), settings.ModelInput{Name: "deepseek-chat"}); !errors.Is(err, settings.ErrConflict) {
		t.Fatalf("duplicate err = %v", err)
	}
	if err := repo.ReplaceWindows(context.Background(), m.ID, []pricing.Window{{
		Label: "peak", DaysMask: 31, StartMinute: 540, EndMinute: 720,
	}}); err != nil {
		t.Fatal(err)
	}
	if len(repo.Snapshot().Models) != 1 || len(repo.Snapshot().Models[0].Windows) != 1 {
		t.Fatalf("snapshot models = %+v", repo.Snapshot().Models)
	}
	if q := repo.Snapshot().Table.Quote("deepseek-chat", time.Now(), time.UTC, usage.Usage{PromptTokens: 1}); !q.Priced {
		t.Fatal("table not rebuilt")
	}
	if err := repo.ReplaceWindows(context.Background(), m.ID, []pricing.Window{{
		Label: "x", DaysMask: 0, StartMinute: 60, EndMinute: 60,
	}}); err == nil {
		t.Fatal("want validation error for empty days/equal minutes")
	}
}

func TestSetPassword(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.SetPassword(ctx, "ignored", "hunter22"); err != nil {
		t.Fatalf("set initial password: %v", err)
	}
	if !settings.VerifyPassword(repo.Snapshot().AdminPasswordHash, "hunter22") {
		t.Fatal("password not stored")
	}
	if err := repo.SetPassword(ctx, "wrong", "hunter33"); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if err := repo.SetPassword(ctx, "hunter22", "short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := repo.SetPassword(ctx, "hunter22", "hunter33"); err != nil {
		t.Fatal(err)
	}
	if !settings.VerifyPassword(repo.Snapshot().AdminPasswordHash, "hunter33") {
		t.Fatal("password not rotated")
	}
}

func TestUpdateModelReasoningFallback(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	reasoning := 0.9
	m, err := repo.CreateModel(ctx, settings.ModelInput{
		Name: "m", Input: 0.1, Output: 0.2, Reasoning: &reasoning,
	})
	if err != nil {
		t.Fatal(err)
	}
	newName := "m-renamed"
	input := 0.3
	var clearReasoning *float64
	if err := repo.UpdateModel(ctx, m.ID, settings.ModelPatch{
		Name: &newName, Input: &input, Reasoning: &clearReasoning,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "m-renamed" || got[0].Input != 0.3 || got[0].Reasoning != nil {
		t.Fatalf("models = %+v", got)
	}
	if err := repo.UpdateModel(ctx, 999, settings.ModelPatch{}); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("missing model err = %v", err)
	}
	if err := repo.DeleteModel(ctx, m.ID); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteModel(ctx, m.ID); !errors.Is(err, settings.ErrNotFound) {
		t.Fatalf("double delete err = %v", err)
	}
}

func TestApplyPatchValidatesBeforeWriting(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.SetPassword(ctx, "", "hunter22"); err != nil {
		t.Fatal(err)
	}

	badTZ := "Mars/Olympus"
	err := repo.ApplyPatch(ctx,
		settings.Patch{Timezone: &badTZ},
		&settings.PasswordChange{Current: "hunter22", New: "newpass123"})
	var ve *settings.ValidationError
	if !errors.As(err, &ve) || ve.Fields["timezone"] == "" {
		t.Fatalf("err = %v", err)
	}
	hash := repo.Snapshot().AdminPasswordHash
	if !settings.VerifyPassword(hash, "hunter22") || settings.VerifyPassword(hash, "newpass123") {
		t.Fatal("failed patch changed the password")
	}

	retention := 30
	if err := repo.ApplyPatch(ctx,
		settings.Patch{RetentionDays: &retention},
		&settings.PasswordChange{Current: "hunter22", New: "newpass123"}); err != nil {
		t.Fatal(err)
	}
	snap := repo.Snapshot()
	if snap.RetentionDays != 30 || !settings.VerifyPassword(snap.AdminPasswordHash, "newpass123") {
		t.Fatalf("snapshot = %+v", snap)
	}
}

func TestClearPassword(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if err := repo.SetPassword(ctx, "", "hunter22"); err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplyPatch(ctx, settings.Patch{}, &settings.PasswordChange{Current: "wrong", Clear: true}); err == nil {
		t.Fatal("wrong current password accepted")
	}
	if repo.Snapshot().AdminPasswordHash == "" {
		t.Fatal("failed clear dropped the password")
	}
	if err := repo.ApplyPatch(ctx, settings.Patch{}, &settings.PasswordChange{Current: "hunter22", Clear: true}); err != nil {
		t.Fatal(err)
	}
	if repo.Snapshot().AdminPasswordHash != "" {
		t.Fatal("password not cleared")
	}
	if err := repo.ApplyPatch(ctx, settings.Patch{}, &settings.PasswordChange{New: "x", Clear: true}); err == nil {
		t.Fatal("set and clear together accepted")
	}
}

func TestImportModelsCreatesMissingZeroRates(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()
	if _, err := repo.CreateModel(ctx, settings.ModelInput{Name: "existing", Input: 1}); err != nil {
		t.Fatal(err)
	}
	created, skipped, err := repo.ImportModels(ctx,
		[]string{" new-a ", "existing", "new-a", "new-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(created) != 2 || created[0].Name != "new-a" || created[1].Name != "new-b" {
		t.Fatalf("created=%+v", created)
	}
	if len(skipped) != 1 || skipped[0] != "existing" {
		t.Fatalf("skipped=%v", skipped)
	}
	for _, m := range created {
		if m.ID == 0 {
			t.Fatalf("created model without id: %+v", m)
		}
		if m.Input != 0 || m.Output != 0 || m.CacheRead != 0 || m.CacheWrite != 0 || m.Reasoning != nil || len(m.Windows) != 0 {
			t.Fatalf("rates = %+v", m)
		}
	}
	if _, _, err := repo.ImportModels(ctx, make([]string, 101)); !errors.As(err, new(*settings.ValidationError)) {
		t.Fatalf("limit err = %v", err)
	}
	if _, _, err := repo.ImportModels(ctx, []string{"   ", ""}); err == nil {
		t.Fatal("empty names must error")
	}
	long := strings.Repeat("x", 201)
	var ve *settings.ValidationError
	if _, _, err := repo.ImportModels(ctx, []string{long}); !errors.As(err, &ve) || ve.Fields["names"] == "" {
		t.Fatalf("long name err = %v", err)
	}

	// Imported rows are persisted with zero rates and the existing model keeps
	// its price.
	models, err := repo.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 3 {
		t.Fatalf("models = %+v", models)
	}
	for _, m := range models {
		switch m.Name {
		case "existing":
			if m.Input != 1 {
				t.Fatalf("existing rate = %+v", m)
			}
		case "new-a", "new-b":
			if m.Input != 0 || m.Reasoning != nil {
				t.Fatalf("imported = %+v", m)
			}
		}
	}

	// A second import skips everything already present.
	created, skipped, err = repo.ImportModels(ctx, []string{"new-a", "new-b"})
	if err != nil || len(created) != 0 || len(skipped) != 2 {
		t.Fatalf("reimport created=%v skipped=%v err=%v", created, skipped, err)
	}
	if skipped[0] != "new-a" || skipped[1] != "new-b" {
		t.Fatalf("reimport skipped order = %v", skipped)
	}
}

func TestImportModelsConcurrentCallsSkipConflicts(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	const workers = 4
	var wg sync.WaitGroup
	created := make([]int, workers)
	errs := make([]error, workers)
	skipped := make([]int, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			models, skippedNames, err := repo.ImportModels(ctx, []string{"race-a", "race-b", "race-a"})
			created[i], skipped[i], errs[i] = len(models), len(skippedNames), err
		}(i)
	}
	wg.Wait()

	totalCreated, totalSkipped := 0, 0
	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d: %v", i, errs[i])
		}
		totalCreated += created[i]
		totalSkipped += skipped[i]
	}
	if totalCreated != 2 || totalSkipped != 2*(workers-1) {
		t.Fatalf("created=%v skipped=%v", created, skipped)
	}

	models, err := repo.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 2 {
		t.Fatalf("models = %+v", models)
	}
}
