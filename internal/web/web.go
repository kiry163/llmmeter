package web

import (
	_ "embed"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/kiry163/llmmeter/internal/store"
)

//go:embed page.html
var pageHTML []byte

func New(st *store.Store, loc *time.Location) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(pageHTML)
	})
	mux.HandleFunc("/api/summary", func(w http.ResponseWriter, r *http.Request) {
		from, to := rangeParams(r, loc)
		sum, err := st.Summary(from, to)
		writeJSON(w, sum, err)
	})
	mux.HandleFunc("/api/daily", func(w http.ResponseWriter, r *http.Request) {
		from, to := rangeParams(r, loc)
		rows, err := st.Daily(from, to)
		writeJSON(w, rows, err)
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		from, to := rangeParams(r, loc)
		rows, err := st.ByModel(from, to)
		writeJSON(w, rows, err)
	})
	mux.HandleFunc("/api/requests", func(w http.ResponseWriter, r *http.Request) {
		from, to := rangeParams(r, loc)
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if limit <= 0 || limit > 200 {
			limit = 50
		}
		rows, err := st.Requests(from, to, r.URL.Query().Get("model"), limit, offset)
		writeJSON(w, rows, err)
	})
	mux.HandleFunc("/api/export.csv", func(w http.ResponseWriter, r *http.Request) {
		from, to := rangeParams(r, loc)
		rows, err := st.AllForExport(from, to, r.URL.Query().Get("model"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="llmmeter.csv"`)
		cw := csv.NewWriter(w)
		cw.Write([]string{"started", "day", "path", "model", "status", "prompt", "completion",
			"cache_read", "reasoning", "window", "cost_usd", "usage_status"})
		for _, it := range rows {
			cw.Write([]string{
				it.StartedAt.Format(time.RFC3339), it.LocalDay, it.Path, it.Model,
				strconv.Itoa(it.Status), strconv.FormatInt(it.Usage.Prompt, 10),
				strconv.FormatInt(it.Usage.Completion, 10), strconv.FormatInt(it.Usage.CacheRead, 10),
				strconv.FormatInt(it.Usage.Reasoning, 10), it.WindowLabel,
				fmt.Sprintf("%.9f", float64(it.CostNanoUSD)/1e9), it.UsageStatus,
			})
		}
		cw.Flush()
	})
	return mux
}

func rangeParams(r *http.Request, loc *time.Location) (string, string) {
	now := time.Now().In(loc)
	to := r.URL.Query().Get("to")
	if to == "" {
		to = now.Format("2006-01-02")
	}
	from := r.URL.Query().Get("from")
	if from == "" {
		from = now.AddDate(0, 0, -6).Format("2006-01-02")
	}
	return from, to
}

func writeJSON(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	json.NewEncoder(w).Encode(v)
}
