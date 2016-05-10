package queue

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"github.com/streadway/amqp"
)

var consumerSeq uint64

type AMQPBroker struct {
	conn *amqp.Connection
	ch   *amqp.Channel
}

func NewAMQPBroker(url string) (Broker, error) {
	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to RabbitMQ: %s", err)
	}

	ch, err := conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open a channel: %s", err)
	}

	return &AMQPBroker{conn: conn, ch: ch}, nil
}

func (b *AMQPBroker) Queue(name string) (Queue, error) {
	q, err := b.ch.QueueDeclare(
		name,  // name
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)

	if err != nil {
		return nil, err
	}

	return &AMQPQueue{conn: b.conn, ch: b.ch, queue: q}, nil
}

func (b *AMQPBroker) Close() error {
	if err := b.ch.Close(); err != nil {
		return err
	}

	if err := b.conn.Close(); err != nil {
		return err
	}

	return nil
}

type AMQPQueue struct {
	conn  *amqp.Connection
	ch    *amqp.Channel
	queue amqp.Queue
}

func (q *AMQPQueue) Publish(j *Job) error {
	if len(j.raw) == 0 {
		return fmt.Errorf("invalid empty job")
	}

	return q.ch.Publish(
		"",           // exchange
		q.queue.Name, // routing key
		false,        // mandatory
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			MessageId:    j.ID,
			Priority:     uint8(j.Priority),
			Timestamp:    j.Timestamp,
			ContentType:  string(j.contentType),
			Body:         j.raw,
		})
}

func (q *AMQPQueue) PublishDelayed(j *Job, delay time.Duration) error {
	ttl := delay / time.Millisecond
	delayedQueue, err := q.ch.QueueDeclare(
		j.ID,  // name
		true,  // durable
		true,  // delete when unused
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    "",
			"x-dead-letter-routing-key": q.queue.Name,
			"x-message-ttl":             int32(ttl),
			"x-expires":                 int32(ttl) * 2,
		},
	)
	if err != nil {
		return err
	}

	return q.ch.Publish(
		"",
		delayedQueue.Name,
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			MessageId:    j.ID,
			Priority:     uint8(j.Priority),
			Timestamp:    j.Timestamp,
			ContentType:  string(j.contentType),
			Body:         j.raw,
		},
	)
}

func (q *AMQPQueue) Transaction(txcb TxCallback) error {
	if err := q.ch.Tx(); err != nil {
		return err
	}

	err := txcb(q)
	if err != nil {
		return q.ch.TxRollback()
	}

	return q.ch.TxCommit()
}

func (q *AMQPQueue) Consume() (JobIter, error) {
	ch, err := q.conn.Channel()
	if err != nil {
		return nil, fmt.Errorf("failed to open a channel: %s", err)
	}

	// enforce prefetching only one job, if this is removed the whole queue
	// will be consumed.
	if err := ch.Qos(1, 0, false); err != nil {
		return nil, err
	}

	id := q.consumeID()
	c, err := ch.Consume(q.queue.Name, id, false, false, false, false, nil)
	if err != nil {
		return nil, err
	}

	return &AMQPJobIter{id: id, ch: ch, c: c}, nil
}

func (q *AMQPQueue) consumeID() string {
	return fmt.Sprintf("%s-%s-%d",
		os.Args[0],
		q.queue.Name,
		atomic.AddUint64(&consumerSeq, 1),
	)
}

type AMQPJobIter struct {
	id string
	ch *amqp.Channel
	c  <-chan amqp.Delivery
}

func (i *AMQPJobIter) Next() (*Job, error) {
	d := <-i.c

	return fromDelivery(&d), nil
}

func (i *AMQPJobIter) Close() error {
	if err := i.ch.Cancel(i.id, false); err != nil {
		return err
	}

	return i.ch.Close()
}

func fromDelivery(d *amqp.Delivery) *Job {
	j := NewJob()
	j.ID = d.MessageId
	j.Priority = Priority(d.Priority)
	j.Timestamp = d.Timestamp
	j.contentType = contentType(d.ContentType)
	j.acknowledger = d.Acknowledger
	j.tag = d.DeliveryTag
	j.raw = d.Body

	return j
}
