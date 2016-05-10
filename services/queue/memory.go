package queue

import (
	"sync"
	"time"
)

type memoryBroker struct {
}

func NewMemoryBroker() Broker {
	return &memoryBroker{}
}

func (b *memoryBroker) Queue(name string) (Queue, error) {
	return &memoryQueue{jobs: make([]*Job, 0, 10)}, nil
}

func (b *memoryBroker) Close() error {
	return nil
}

type memoryQueue struct {
	jobs []*Job
	sync.RWMutex
	idx                int
	publishImmediately bool
}

func (q *memoryQueue) Publish(job *Job) error {
	q.Lock()
	defer q.Unlock()
	q.jobs = append(q.jobs, job)
	return nil
}

func (q *memoryQueue) PublishDelayed(job *Job, delay time.Duration) error {
	if q.publishImmediately {
		return q.Publish(job)
	}
	go func() {
		<-time.After(delay)
		q.Publish(job)
	}()
	return nil
}

func (q *memoryQueue) Transaction(txcb TxCallback) error {
	txQ := &memoryQueue{jobs: make([]*Job, 0, 10), publishImmediately: true}
	if err := txcb(txQ); err != nil {
		return nil
	}

	q.jobs = append(q.jobs, txQ.jobs...)
	return nil
}

func (q *memoryQueue) Consume() (JobIter, error) {
	return &memoryJobIter{&q.jobs, &q.idx, &q.RWMutex}, nil
}

type memoryJobIter struct {
	jobs *[]*Job
	idx  *int
	*sync.RWMutex
}

type mockAcknowledger struct{}

func (*mockAcknowledger) Ack(tag uint64, multiple bool) error {
	return nil
}
func (*mockAcknowledger) Nack(tag uint64, multiple bool, requeue bool) error {
	return nil
}
func (*mockAcknowledger) Reject(tag uint64, requeue bool) error {
	return nil
}

func (i *memoryJobIter) Next() (*Job, error) {
	i.Lock()
	defer i.Unlock()
	if len(*i.jobs) <= *i.idx {
		return nil, nil
	}
	j := (*i.jobs)[*i.idx]
	(*i.idx)++
	j.tag = 1
	j.acknowledger = &mockAcknowledger{}
	return j, nil
}

func (i *memoryJobIter) Close() error {
	return nil
}
