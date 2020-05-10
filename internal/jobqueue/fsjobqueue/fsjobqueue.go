// Package fsjobqueue implements a filesystem-backed job queue. It implements
// the interfaces in package jobqueue.
//
// Jobs are stored in the file system, using the `jsondb` package. However,
// this package does not use the file system as a database, but keeps some
// state in memory. This means that access to a given directory must be
// exclusive to only one `fsJobQueue` object at a time. A single `fsJobQueue`
// can be safely accessed from multiple goroutines, though.
//
// Data is stored non-reduntantly. Any data structure necessary for efficient
// access (e.g., dependants) are kept in memory.
package fsjobqueue

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/osbuild/osbuild-composer/internal/jobqueue"
	"github.com/osbuild/osbuild-composer/internal/jsondb"
)

type fsJobQueue struct {
	// Protects all fields of this struct. In particular, it ensures
	// transactions on `db` are atomic. All public functions except
	// JobStatus hold it while they're running. Dequeue() releases it
	// briefly while waiting on pending channels.
	mu sync.Mutex

	db *jsondb.JSONDatabase

	// Maps job types to channels of job ids for that type. Only access
	// through pendingChannel(), which ensures that a map for the given job
	// typ exists.
	pending map[string]chan uuid.UUID

	// Maps job ids to the jobs that depend on it, if any of those
	// dependants have not yet finished.
	dependants map[uuid.UUID][]uuid.UUID
}

// On-disk job struct. Contains all necessary (but non-redundant) information
// about a job. These are not held in memory by the job queue, but
// (de)serialized on each access.
type job struct {
	Id           uuid.UUID       `json:"id"`
	Type         string          `json:"type"`
	Args         json.RawMessage `json:"args,omitempty"`
	Dependencies []uuid.UUID     `json:"dependencies"`
	Result       json.RawMessage `json:"result,omitempty"`

	QueuedAt   time.Time `json:"queued_at,omitempty"`
	StartedAt  time.Time `json:"started_at,omitempty"`
	FinishedAt time.Time `json:"finished_at,omitempty"`
}

// Create a new fsJobQueue object for `dir`. This object must have exclusive
// access to `dir`. If `dir` contains jobs created from previous runs, they are
// loaded and rescheduled to run if necessary.
func New(dir string) (*fsJobQueue, error) {
	q := &fsJobQueue{
		db:         jsondb.New(dir, 0600),
		pending:    make(map[string]chan uuid.UUID),
		dependants: make(map[uuid.UUID][]uuid.UUID),
	}

	// Look for jobs that are still pending and build the dependant map.
	ids, err := q.db.List()
	if err != nil {
		return nil, fmt.Errorf("error listing jobs: %v", err)
	}
	for _, id := range ids {
		uuid, err := uuid.Parse(id)
		if err != nil {
			return nil, fmt.Errorf("invalid job '%s' in db: %v", id, err)
		}
		j, err := q.readJob(uuid)
		if err != nil {
			return nil, err
		}
		// We only enqueue jobs that were previously pending.
		if j.StartedAt.IsZero() {
			continue
		}
		// Enqueue the job again if all dependencies have finished, or
		// there are none. Otherwise, update dependants so that this
		// check is done again when FinishJob() is called for a
		// dependency.
		n, err := q.countFinishedJobs(j.Dependencies)
		if err != nil {
			return nil, err
		}
		if n == len(j.Dependencies) {
			q.pendingChannel(j.Type) <- j.Id
		} else {
			for _, dep := range j.Dependencies {
				q.dependants[dep] = append(q.dependants[dep], j.Id)
			}
		}
	}

	return q, nil
}

func (q *fsJobQueue) Enqueue(jobType string, args interface{}, dependencies []uuid.UUID) (uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	var j = job{
		Id:           uuid.New(),
		Type:         jobType,
		Dependencies: uniqueUUIDList(dependencies),
		QueuedAt:     time.Now(),
	}

	var err error
	j.Args, err = json.Marshal(args)
	if err != nil {
		return uuid.Nil, fmt.Errorf("error marshaling job arguments: %v", err)
	}

	// Verify dependencies and check how many of them are already finished.
	finished, err := q.countFinishedJobs(j.Dependencies)
	if err != nil {
		return uuid.Nil, err
	}

	// Write the job before updating in-memory state, so that the latter
	// doesn't become corrupt when writing fails.
	err = q.db.Write(j.Id.String(), j)
	if err != nil {
		return uuid.Nil, fmt.Errorf("cannot write job: %v:", err)
	}

	// If all dependencies have finished, or there are none, queue the job.
	// Otherwise, update dependants so that this check is done again when
	// FinishJob() is called for a dependency.
	if finished == len(j.Dependencies) {
		q.pendingChannel(j.Type) <- j.Id
	} else {
		for _, id := range j.Dependencies {
			q.dependants[id] = append(q.dependants[id], j.Id)
		}
	}

	return j.Id, nil
}

func (q *fsJobQueue) Dequeue(ctx context.Context, jobTypes []string, args interface{}) (uuid.UUID, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	// Return early if the context is already canceled.
	if err := ctx.Err(); err != nil {
		return uuid.Nil, err
	}

	chans := q.pendingChannels(jobTypes)

	// Unlock the mutex while polling channels, so that multiple goroutines
	// can wait at the same time.
	q.mu.Unlock()
	id, err := selectUUIDChannel(ctx, chans)
	q.mu.Lock()

	if err != nil {
		return uuid.Nil, err
	}

	j, err := q.readJob(id)
	if err != nil {
		return uuid.Nil, err
	}

	err = json.Unmarshal(j.Args, args)
	if err != nil {
		return uuid.Nil, fmt.Errorf("error unmarshaling arguments for job '%s': %v", j.Id, err)
	}

	j.StartedAt = time.Now()

	err = q.db.Write(id.String(), j)
	if err != nil {
		return uuid.Nil, fmt.Errorf("error writing job %s: %v", id, err)
	}

	return j.Id, nil
}

func (q *fsJobQueue) FinishJob(id uuid.UUID, result interface{}) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	j, err := q.readJob(id)
	if err != nil {
		return err
	}

	if j.StartedAt.IsZero() || !j.FinishedAt.IsZero() {
		return jobqueue.ErrNotRunning
	}

	j.FinishedAt = time.Now()

	j.Result, err = json.Marshal(result)
	if err != nil {
		return fmt.Errorf("error marshaling result: %v", err)
	}

	// Write before notifying dependants, because it will be read again.
	err = q.db.Write(id.String(), j)
	if err != nil {
		return fmt.Errorf("error writing job %s: %v", id, err)
	}

	for _, depid := range q.dependants[id] {
		dep, err := q.readJob(depid)
		if err != nil {
			return err
		}
		n, err := q.countFinishedJobs(dep.Dependencies)
		if err != nil {
			return err
		}
		if n == len(dep.Dependencies) {
			q.pendingChannel(dep.Type) <- dep.Id
		}
	}
	delete(q.dependants, id)

	return nil
}

func (q *fsJobQueue) JobStatus(id uuid.UUID, result interface{}) (queued, started, finished time.Time, err error) {
	var j *job

	j, err = q.readJob(id)
	if err != nil {
		return
	}

	if !j.FinishedAt.IsZero() {
		err = json.Unmarshal(j.Result, result)
		if err != nil {
			err = fmt.Errorf("error unmarshaling result for job '%s': %v", id, err)
			return
		}
	}

	queued = j.QueuedAt
	started = j.StartedAt
	finished = j.FinishedAt

	return
}

// Returns the number of finished jobs in `ids`.
func (q *fsJobQueue) countFinishedJobs(ids []uuid.UUID) (int, error) {
	n := 0
	for _, id := range ids {
		j, err := q.readJob(id)
		if err != nil {
			return 0, err
		}
		if !j.FinishedAt.IsZero() {
			n += 1
		}
	}

	return n, nil
}

// Reads job with `id`. This is a thin wrapper around `q.db.Read`, which
// returns the job directly, or and error if a job with `id` does not exist.
func (q *fsJobQueue) readJob(id uuid.UUID) (*job, error) {
	var j job
	exists, err := q.db.Read(id.String(), &j)
	if err != nil {
		return nil, fmt.Errorf("error reading job '%s': %v", id, err)
	}
	if !exists {
		// return corrupt database?
		return nil, jobqueue.ErrNotExist
	}
	return &j, nil
}

// Safe access to the pending channel for `jobType`. Channels are created on
// demand.
func (q *fsJobQueue) pendingChannel(jobType string) chan uuid.UUID {
	c, exists := q.pending[jobType]
	if !exists {
		c = make(chan uuid.UUID, 100)
		q.pending[jobType] = c
	}

	return c
}

// Same as pendingChannel(), but for multiple job types.
func (q *fsJobQueue) pendingChannels(jobTypes []string) []chan uuid.UUID {
	chans := make([]chan uuid.UUID, len(jobTypes))

	for i, jt := range jobTypes {
		c, exists := q.pending[jt]
		if !exists {
			c = make(chan uuid.UUID, 100)
			q.pending[jt] = c
		}
		chans[i] = c
	}

	return chans
}

// Sorts and removes duplicates from `ids`.
func uniqueUUIDList(ids []uuid.UUID) []uuid.UUID {
	s := map[uuid.UUID]bool{}
	for _, id := range ids {
		s[id] = true
	}

	l := []uuid.UUID{}
	for id := range s {
		l = append(l, id)
	}

	sort.Slice(l, func(i, j int) bool {
		for b := 0; b < 16; b++ {
			if l[i][b] < l[j][b] {
				return true
			}
		}
		return false
	})

	return l
}

// Select on a list of `chan uuid.UUID`s. Returns an error if one of the
// channels is closed.
//
// Uses reflect.Select(), because the `select` statement cannot operate on an
// unknown amount of channels.
func selectUUIDChannel(ctx context.Context, chans []chan uuid.UUID) (uuid.UUID, error) {
	cases := []reflect.SelectCase{
		{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(ctx.Done()),
		},
	}
	for _, c := range chans {
		cases = append(cases, reflect.SelectCase{
			Dir:  reflect.SelectRecv,
			Chan: reflect.ValueOf(c),
		})
	}

	chosen, value, recvOK := reflect.Select(cases)
	if !recvOK {
		if chosen == 0 {
			return uuid.Nil, ctx.Err()
		} else {
			return uuid.Nil, errors.New("channel was closed unexpectedly")
		}
	}

	return value.Interface().(uuid.UUID), nil
}