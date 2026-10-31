package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// queueSize is the capacity of the ops channel for the writer goroutine.
	queueSize = 1024
	// maxBatch caps how many records go into one insert transaction.
	maxBatch = 128
	// batchInterval bounds how long a record waits before being written.
	batchInterval = 10 * time.Millisecond
)

// Store is the single-writer SQLite store used by the gateway and the metrics
// collector. All writes go through one goroutine so inserts are batched and
// never serialize against each other at the SQLite level.
type Store struct {
	db *sql.DB
	// path is retained so sidecar permissions can be re-applied after writes.
	path string
	ops  chan op
	done chan struct{}

	// mu guards closed and the send on ops; callers hold at least a read
	// lock while enqueueing so Close cannot race into a send on a dead loop.
	mu     sync.RWMutex
	closed bool

	errMu sync.Mutex
	err   error
}

// Open opens the database at path, applies the schema and starts the writer
// goroutine.
func Open(path string) (*Store, error) {
	db, err := OpenDB(path)
	if err != nil {
		return nil, err
	}
	s := &Store{
		db:   db,
		path: path,
		ops:  make(chan op, queueSize),
		done: make(chan struct{}),
	}
	go s.loop()
	return s, nil
}

// DB exposes the underlying handle for read queries.
func (s *Store) DB() *sql.DB { return s.db }

type opKind int

const (
	opRecord opKind = iota
	opMinute
	opPrune
	opFlush
	opQuit
)

type op struct {
	kind        opKind
	rec         Record
	minute      MinuteDelta
	pruneBefore time.Time
	ack         chan error
}

// EnqueueRecord queues one request row for insertion.
func (s *Store) EnqueueRecord(r Record) { s.enqueue(op{kind: opRecord, rec: r}) }

// EnqueueMinute queues one incremental aggregate update.
func (s *Store) EnqueueMinute(d MinuteDelta) { s.enqueue(op{kind: opMinute, minute: d}) }

// EnqueuePrune queues deletion of requests started before before.
func (s *Store) EnqueuePrune(before time.Time) {
	s.enqueue(op{kind: opPrune, pruneBefore: before})
}

// enqueue blocks until the writer accepts the op, providing backpressure when
// the queue is full. Enqueues after Close are silently dropped.
func (s *Store) enqueue(o op) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.closed {
		return
	}
	s.ops <- o
}

// QueueDepth reports the approximate number of pending ops. Ops already
// accumulated into the current batch are not counted, so treat it as a health
// indicator rather than an exact backlog size.
func (s *Store) QueueDepth() int { return len(s.ops) }

// Flush waits until every op enqueued before the call has been applied.
func (s *Store) Flush() error {
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return s.takeErr()
	}
	ack := make(chan error, 1)
	s.ops <- op{kind: opFlush, ack: ack}
	s.mu.RUnlock()
	return <-ack
}

// Close flushes pending writes, stops the writer, checkpoints the WAL and
// closes the database. A pending prune is abandoned rather than drained, so
// shutdown is bounded. It is safe to call more than once.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ack := make(chan error, 1)
	s.ops <- op{kind: opQuit, ack: ack}
	s.mu.Unlock()

	quitErr := <-ack
	<-s.done
	// Anything noted after the quit barrier is reported too.
	lateErr := s.takeErr()
	_, checkpointErr := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	secureDBFiles(s.path)
	closeErr := s.db.Close()
	if checkpointErr != nil {
		checkpointErr = fmt.Errorf("wal checkpoint: %w", checkpointErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close database: %w", closeErr)
	}
	return errors.Join(quitErr, lateErr, checkpointErr, closeErr)
}

// loop consumes ops until opQuit. Records are collected into a batch that is
// written when it reaches maxBatch or batchInterval elapses. A prune deletes
// at most one chunk per op or tick, so inserts and minute upserts interleave
// with a large cleanup instead of waiting for it to finish.
func (s *Store) loop() {
	defer close(s.done)

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	batch := make([]Record, 0, maxBatch)
	flushBatch := func() {
		s.insertRecords(batch)
		batch = batch[:0]
		secureDBFiles(s.path)
	}

	// pruneCutoff non-empty means a prune is in progress; each call removes
	// one bounded chunk and clears the marker once nothing is left.
	var pruneCutoff string
	runPrune := func() {
		if pruneCutoff == "" {
			return
		}
		more, err := s.pruneChunk(pruneCutoff)
		if err != nil {
			s.note(err)
			pruneCutoff = ""
			return
		}
		if !more {
			pruneCutoff = ""
		}
	}

	for {
		select {
		case o := <-s.ops:
			switch o.kind {
			case opRecord:
				batch = append(batch, o.rec)
				if len(batch) >= maxBatch {
					flushBatch()
				}
			case opMinute:
				s.note(s.upsertMinute(&o.minute))
			case opPrune:
				pruneCutoff = timestampText(o.pruneBefore)
				runPrune()
			case opFlush:
				flushBatch()
				// Flush is a barrier: finish the pending prune so callers
				// that enqueued one observe it fully applied.
				for pruneCutoff != "" {
					runPrune()
				}
				o.ack <- s.takeErr()
			case opQuit:
				flushBatch()
				o.ack <- s.takeErr()
				return
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flushBatch()
			}
			runPrune()
		}
	}
}

// insertRecords writes recs as one transaction. If the batch fails, every row
// is retried on its own so a single offending record cannot discard the rest;
// each per-row failure is noted.
func (s *Store) insertRecords(recs []Record) {
	if len(recs) == 0 {
		return
	}
	if err := s.insertBatch(recs); err == nil {
		return
	}
	for i := range recs {
		if err := s.insertBatch(recs[i : i+1]); err != nil {
			s.note(err)
		}
	}
}

// insertBatch writes recs in a single transaction using one prepared
// statement.
func (s *Store) insertBatch(recs []Record) error {
	if len(recs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin insert batch: %w", err)
	}
	stmt, err := tx.Prepare(insertRequestSQL)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("prepare insert request: %w", err)
	}
	for i := range recs {
		if _, err := stmt.Exec(insertArgs(&recs[i])...); err != nil {
			stmt.Close()
			tx.Rollback()
			return fmt.Errorf("insert request: %w", err)
		}
	}
	if err := stmt.Close(); err != nil {
		tx.Rollback()
		return fmt.Errorf("close insert statement: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit insert batch: %w", err)
	}
	return nil
}

// execRetry executes one statement, retrying a single time to ride out a
// transient lock before reporting the failure.
func (s *Store) execRetry(query string, args ...any) (sql.Result, error) {
	res, err := s.db.Exec(query, args...)
	if err == nil {
		return res, nil
	}
	res, retryErr := s.db.Exec(query, args...)
	if retryErr != nil {
		return nil, errors.Join(err, retryErr)
	}
	return res, nil
}

// upsertMinute accumulates d into its traffic_minutes row.
func (s *Store) upsertMinute(d *MinuteDelta) error {
	if _, err := s.execRetry(upsertMinuteSQL, minuteArgs(d)...); err != nil {
		return fmt.Errorf("upsert traffic minute: %w", err)
	}
	return nil
}

// pruneChunkRows is the maximum number of detail rows one prune statement
// deletes; pruneSQL carries the same literal.
const pruneChunkRows = 10000

// pruneChunk deletes one bounded chunk of requests started before the cutoff.
// more reports whether the chunk was full, i.e. another pass is needed.
func (s *Store) pruneChunk(cutoff string) (more bool, err error) {
	res, err := s.execRetry(pruneSQL, cutoff)
	if err != nil {
		return false, fmt.Errorf("prune requests: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("prune requests rows: %w", err)
	}
	return n >= pruneChunkRows, nil
}

// ProbeWritable verifies that the database accepts a write transaction without
// going through the writer goroutine. It opens a short BEGIN IMMEDIATE and
// rolls it back, so it only proves the write lock can be acquired.
func (s *Store) ProbeWritable(ctx context.Context) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("probe writable: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("probe writable: %w", err)
	}
	// Roll back with an independent deadline so a cancelled request still
	// releases the write lock before the connection returns to the pool.
	rollbackCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := conn.ExecContext(rollbackCtx, "ROLLBACK"); err != nil {
		return fmt.Errorf("probe writable rollback: %w", err)
	}
	return nil
}

// LastError returns the most recent writer error without clearing it.
func (s *Store) LastError() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}

// note records a writer error so Flush and Close can surface it.
func (s *Store) note(err error) {
	if err == nil {
		return
	}
	s.errMu.Lock()
	s.err = errors.Join(s.err, err)
	s.errMu.Unlock()
}

// takeErr returns the accumulated writer errors and clears them, so a reported
// failure is surfaced exactly once.
func (s *Store) takeErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	err := s.err
	s.err = nil
	return err
}
