package timeoutMQ

import (
	"context"
	"crypto/md5"
	"encoding/json"
	"fmt"
	"reflect"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type TimeoutMQ struct {
	conn             *amqp.Connection
	ch               *amqp.Channel
	connectionString string
}

type TimeoutMQInterface interface {
	Timeout(object interface{}, duration time.Duration) error
	Recieve(object interface{}) (<-chan interface{}, error)
}

type TimeoutMessage struct {
	Type string      `json:"type"`
	Data interface{} `json:"data"`
}

func Dial(connectionString string) (*TimeoutMQ, error) {
	conn, err := amqp.Dial(connectionString)
	if err != nil {
		return &TimeoutMQ{}, err
	}

	ch, err := conn.Channel()
	if err != nil {
		return &TimeoutMQ{}, err
	}

	return &TimeoutMQ{
		conn:             conn,
		ch:               ch,
		connectionString: connectionString,
	}, nil
}

// generateQueueName creates a deterministic queue name based on the object type
func (mq *TimeoutMQ) generateQueueName(object interface{}) string {
	objectType := reflect.TypeOf(object)
	if objectType.Kind() == reflect.Ptr {
		objectType = objectType.Elem()
	}

	typeName := fmt.Sprintf("%s.%s", objectType.PkgPath(), objectType.Name())
	hash := md5.Sum([]byte(typeName))

	// Create an informative name with the struct name and hash for collision avoidance
	structName := objectType.Name()
	return fmt.Sprintf("timeout_%s_%x", structName, hash[:4])
}

// setupQueues creates the necessary exchanges and queues for timeout functionality
func (mq *TimeoutMQ) setupQueues(queueName string) error {
	if err := mq.checkConnection(); err != nil {
		return err
	}

	// Declare the dead letter exchange (where expired messages go)
	dlxName := queueName + "_dlx"
	err := mq.ch.ExchangeDeclare(
		dlxName,
		"direct",
		true,  // durable
		false, // auto-delete
		false, // internal
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare dead letter exchange: %w", err)
	}

	// Declare the timeout queue (where messages wait to expire)
	timeoutQueueName := queueName + "_timeout"
	_, err = mq.ch.QueueDeclare(
		timeoutQueueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		amqp.Table{
			"x-dead-letter-exchange":    dlxName,
			"x-dead-letter-routing-key": queueName,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to declare timeout queue: %w", err)
	}

	// Declare the result queue (where expired messages are delivered)
	_, err = mq.ch.QueueDeclare(
		queueName,
		true,  // durable
		false, // delete when unused
		false, // exclusive
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to declare result queue: %w", err)
	}

	// Bind the result queue to the dead letter exchange
	err = mq.ch.QueueBind(
		queueName,
		queueName,
		dlxName,
		false, // no-wait
		nil,   // arguments
	)
	if err != nil {
		return fmt.Errorf("failed to bind result queue: %w", err)
	}

	return nil
}

func (mq *TimeoutMQ) Timeout(object interface{}, duration time.Duration) error {
	if err := mq.checkConnection(); err != nil {
		return err
	}

	queueName := mq.generateQueueName(object)
	if err := mq.setupQueues(queueName); err != nil {
		return err
	}

	// Create timeout message with type information
	objectType := reflect.TypeOf(object)
	if objectType.Kind() == reflect.Ptr {
		objectType = objectType.Elem()
	}

	message := TimeoutMessage{
		Type: fmt.Sprintf("%s.%s", objectType.PkgPath(), objectType.Name()),
		Data: object,
	}

	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("failed to marshal object: %w", err)
	}

	timeoutQueueName := queueName + "_timeout"

	// Calculate expiration in milliseconds
	expiration := fmt.Sprintf("%d", duration.Milliseconds())

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = mq.ch.PublishWithContext(
		ctx,
		"",               // exchange
		timeoutQueueName, // routing key
		false,            // mandatory
		false,            // immediate
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         body,
			Expiration:   expiration,
			DeliveryMode: amqp.Persistent,
		},
	)
	if err != nil {
		return fmt.Errorf("failed to publish timeout message: %w", err)
	}

	return nil
}

func (mq *TimeoutMQ) Recieve(object interface{}) (<-chan interface{}, error) {
	if err := mq.checkConnection(); err != nil {
		return nil, err
	}

	queueName := mq.generateQueueName(object)
	if err := mq.setupQueues(queueName); err != nil {
		return nil, err
	}

	msgs, err := mq.ch.Consume(
		queueName,
		"",    // consumer tag
		false, // auto-ack
		false, // exclusive
		false, // no-local
		false, // no-wait
		nil,   // args
	)
	if err != nil {
		return nil, fmt.Errorf("failed to consume messages: %w", err)
	}

	// Create a channel to return the deserialized objects
	resultChan := make(chan interface{}, 100)

	// Get the type information for deserialization
	objectType := reflect.TypeOf(object)
	if objectType.Kind() == reflect.Ptr {
		objectType = objectType.Elem()
	}

	go func() {
		defer close(resultChan)
		for msg := range msgs {
			var timeoutMsg TimeoutMessage
			if err := json.Unmarshal(msg.Body, &timeoutMsg); err != nil {
				// Log error but continue processing
				fmt.Printf("Failed to unmarshal message: %v\n", err)
				msg.Nack(false, false) // Don't requeue malformed messages
				continue
			}

			// Create a new instance of the object type
			newObj := reflect.New(objectType).Interface()

			// Marshal and unmarshal to convert the data to the correct type
			dataBytes, err := json.Marshal(timeoutMsg.Data)
			if err != nil {
				fmt.Printf("Failed to marshal timeout data: %v\n", err)
				msg.Nack(false, false)
				continue
			}

			if err := json.Unmarshal(dataBytes, newObj); err != nil {
				fmt.Printf("Failed to unmarshal to target type: %v\n", err)
				msg.Nack(false, false)
				continue
			}

			// Send the deserialized object to the result channel
			select {
			case resultChan <- newObj:
				msg.Ack(false)
			default:
				// Channel is full, reject the message
				msg.Nack(false, true)
			}
		}
	}()

	return resultChan, nil
}

// Checks if the connection and channel are open, and reconnects if they are closed.
func (mq *TimeoutMQ) checkConnection() error {
	if mq.ch == nil || mq.ch.IsClosed() {
		if mq.conn == nil || mq.conn.IsClosed() {
			conn, err := amqp.Dial(mq.connectionString)
			if err != nil {
				return err
			}
			mq.conn = conn
		}
		ch, err := mq.conn.Channel()
		if err != nil {
			return err
		}
		mq.ch = ch
	}
	return nil
}

// Close closes the connection and channel
func (mq *TimeoutMQ) Close() error {
	if mq.ch != nil && !mq.ch.IsClosed() {
		if err := mq.ch.Close(); err != nil {
			return err
		}
	}
	if mq.conn != nil && !mq.conn.IsClosed() {
		if err := mq.conn.Close(); err != nil {
			return err
		}
	}
	return nil
}

// Example usage:
/*
package main

import (
	"fmt"
	"time"
	"your-module/timeoutMQ"
)

type MyObject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

func main() {
	mq, err := timeoutMQ.Dial()
	if err != nil {
		panic(err)
	}
	defer mq.Close()

	// Send an object to timeout after 5 seconds
	obj := &MyObject{ID: 1, Name: "Test Object"}
	err = mq.Timeout(obj, 5*time.Second)
	if err != nil {
		panic(err)
	}

	// Receive timed out objects
	ch, err := mq.Recieve(&MyObject{})
	if err != nil {
		panic(err)
	}

	// Wait for the object to timeout and be received
	select {
	case timedOutObj := <-ch:
		fmt.Printf("Received timed out object: %+v\n", timedOutObj)
	case <-time.After(10 * time.Second):
		fmt.Println("No object received within 10 seconds")
	}
}
*/
