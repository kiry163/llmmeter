package store

import (
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
	db   *sql.DB
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
		return s.lastErr()
	}
	ack := make(chan error, 1)
	s.ops <- op{kind: opFlush, ack: ack}
	s.mu.RUnlock()
	return <-ack
}

// Close flushes pending writes, stops the writer, checkpoints the WAL and
// closes the database. It is safe to call more than once.
func (s *Store) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	ack := make(chan error, 1)
	s.ops <- op{kind: opFlush, ack: ack}
	s.ops <- op{kind: opQuit}
	s.mu.Unlock()

	flushErr := <-ack
	<-s.done
	_, checkpointErr := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	closeErr := s.db.Close()
	if checkpointErr != nil {
		checkpointErr = fmt.Errorf("wal checkpoint: %w", checkpointErr)
	}
	if closeErr != nil {
		closeErr = fmt.Errorf("close database: %w", closeErr)
	}
	return errors.Join(flushErr, checkpointErr, closeErr)
}

// loop consumes ops until opQuit. Records are collected into a batch that is
// written when it reaches maxBatch or batchInterval elapses.
func (s *Store) loop() {
	defer close(s.done)

	ticker := time.NewTicker(batchInterval)
	defer ticker.Stop()

	batch := make([]Record, 0, maxBatch)
	flushBatch := func() {
		s.note(s.insertBatch(batch))
		batch = batch[:0]
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
				s.note(s.pruneBefore(o.pruneBefore))
			case opFlush:
				flushBatch()
				o.ack <- s.lastErr()
			case opQuit:
				flushBatch()
				return
			}
		case <-ticker.C:
			if len(batch) > 0 {
				flushBatch()
			}
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

// upsertMinute accumulates d into its traffic_minutes row.
func (s *Store) upsertMinute(d *MinuteDelta) error {
	if _, err := s.db.Exec(upsertMinuteSQL, minuteArgs(d)...); err != nil {
		return fmt.Errorf("upsert traffic minute: %w", err)
	}
	return nil
}

// pruneBefore deletes requests started before the cutoff in chunks of up to
// 10000 rows until none remain.
func (s *Store) pruneBefore(before time.Time) error {
	cutoff := timestampText(before)
	for {
		res, err := s.db.Exec(pruneSQL, cutoff)
		if err != nil {
			return fmt.Errorf("prune requests: %w", err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("prune requests rows: %w", err)
		}
		if n == 0 {
			return nil
		}
	}
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

func (s *Store) lastErr() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.err
}
