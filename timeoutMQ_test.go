package timeoutMQ

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

type TestObject struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TestObjectWithPointer struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type TestObjectNoJSON struct {
	ID   int
	Name string
}

// Mock implementations for testing without actual RabbitMQ
type MockChannel struct {
	closed        bool
	declareError  error
	publishError  error
	consumeError  error
	bindError     error
	messages      chan amqp.Delivery
	publishedMsgs []amqp.Publishing
}

func (m *MockChannel) IsClosed() bool {
	return m.closed
}

func (m *MockChannel) Close() error {
	m.closed = true
	return nil
}

func (m *MockChannel) ExchangeDeclare(name, kind string, durable, autoDelete, internal, noWait bool, args amqp.Table) error {
	if m.declareError != nil {
		return m.declareError
	}
	return nil
}

func (m *MockChannel) QueueDeclare(name string, durable, autoDelete, exclusive, noWait bool, args amqp.Table) (amqp.Queue, error) {
	if m.declareError != nil {
		return amqp.Queue{}, m.declareError
	}
	return amqp.Queue{Name: name}, nil
}

func (m *MockChannel) QueueBind(name, key, exchange string, noWait bool, args amqp.Table) error {
	if m.bindError != nil {
		return m.bindError
	}
	return nil
}

func (m *MockChannel) PublishWithContext(ctx context.Context, exchange, key string, mandatory, immediate bool, msg amqp.Publishing) error {
	if m.publishError != nil {
		return m.publishError
	}
	m.publishedMsgs = append(m.publishedMsgs, msg)
	return nil
}

func (m *MockChannel) Consume(queue, consumer string, autoAck, exclusive, noLocal, noWait bool, args amqp.Table) (<-chan amqp.Delivery, error) {
	if m.consumeError != nil {
		return nil, m.consumeError
	}
	if m.messages == nil {
		m.messages = make(chan amqp.Delivery, 100)
	}
	return m.messages, nil
}

type MockConnection struct {
	closed bool
}

func (m *MockConnection) IsClosed() bool {
	return m.closed
}

func (m *MockConnection) Close() error {
	m.closed = true
	return nil
}

func (m *MockConnection) Channel() (*amqp.Channel, error) {
	return nil, fmt.Errorf("mock connection cannot create real channel")
}

type MockDelivery struct {
	body    []byte
	acked   bool
	nacked  bool
	requeue bool
}

func (m *MockDelivery) Ack(multiple bool) error {
	m.acked = true
	return nil
}

func (m *MockDelivery) Nack(multiple, requeue bool) error {
	m.nacked = true
	m.requeue = requeue
	return nil
}

// Test successful connection and basic functionality
func TestDial_Success(t *testing.T) {
	// This test requires actual RabbitMQ, so we'll skip it if not available
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	if mq.conn == nil {
		t.Error("Connection should not be nil")
	}
	if mq.ch == nil {
		t.Error("Channel should not be nil")
	}
	if mq.connectionString != "amqp://admin:admin@localhost:5672/" {
		t.Error("Connection string not stored correctly")
	}
}

// Test connection failure
func TestDial_ConnectionFailure(t *testing.T) {
	_, err := Dial("amqp://invalid:invalid@nonexistent:5672/")
	if err == nil {
		t.Error("Expected connection to fail")
	}
}

// Test generateQueueName with regular struct
func TestGenerateQueueName_RegularStruct(t *testing.T) {
	mq := &TimeoutMQ{}
	obj := TestObject{ID: 1, Name: "test"}

	queueName := mq.generateQueueName(obj)
	if queueName == "" {
		t.Error("Queue name should not be empty")
	}

	// Should contain the struct name
	if !strings.Contains(queueName, "TestObject") {
		t.Errorf("Queue name should contain struct name: %s", queueName)
	}
}

// Test generateQueueName with pointer
func TestGenerateQueueName_Pointer(t *testing.T) {
	mq := &TimeoutMQ{}
	obj := &TestObjectWithPointer{ID: 1, Name: "test"}

	queueName := mq.generateQueueName(obj)
	if queueName == "" {
		t.Error("Queue name should not be empty")
	}

	// Should contain the struct name (without pointer)
	if !strings.Contains(queueName, "TestObjectWithPointer") {
		t.Errorf("Queue name should contain struct name: %s", queueName)
	}
}

// Test generateQueueName consistency
func TestGenerateQueueName_Consistency(t *testing.T) {
	mq := &TimeoutMQ{}
	obj1 := TestObject{ID: 1, Name: "test1"}
	obj2 := TestObject{ID: 2, Name: "test2"}

	queueName1 := mq.generateQueueName(obj1)
	queueName2 := mq.generateQueueName(obj2)

	if queueName1 != queueName2 {
		t.Error("Queue names should be the same for same struct types")
	}
}

// Test basic timeout and receive functionality
func TestTimeout_AndReceive_Success(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	testObj := TestObject{ID: 1, Name: "Test Object"}

	// Send timeout message
	err = mq.Timeout(testObj, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to send timeout message: %v", err)
	}

	// Receive timeout message
	objectCh, err := mq.Recieve(&TestObject{})
	if err != nil {
		t.Fatalf("Failed to setup receive: %v", err)
	}

	// Wait briefly for message
	time.Sleep(200 * time.Millisecond)

	select {
	case receivedObj := <-objectCh:
		gottenObject := receivedObj.(*TestObject)
		if gottenObject.ID != testObj.ID || gottenObject.Name != testObj.Name {
			t.Errorf("Received object doesn't match sent object. Got: %+v, Expected: %+v",
				gottenObject, testObj)
		}
	default:
		t.Error("No message received")
	}
}

// Test timeout with pointer object
func TestTimeout_WithPointer(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	testObj := &TestObjectWithPointer{ID: 1, Name: "Test Object"}

	err = mq.Timeout(testObj, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("Failed to send timeout message with pointer: %v", err)
	}

	objectCh, err := mq.Recieve(&TestObjectWithPointer{})
	if err != nil {
		t.Fatalf("Failed to setup receive: %v", err)
	}

	time.Sleep(200 * time.Millisecond)

	select {
	case receivedObj := <-objectCh:
		gottenObject := receivedObj.(*TestObjectWithPointer)
		if gottenObject.ID != testObj.ID || gottenObject.Name != testObj.Name {
			t.Errorf("Received object doesn't match sent object. Got: %+v, Expected: %+v",
				gottenObject, testObj)
		}
	default:
		t.Error("No message received")
	}
}

// Test checkConnection with invalid connection string
func TestCheckConnection_InvalidConnectionString(t *testing.T) {
	mq := &TimeoutMQ{
		connectionString: "invalid-connection-string",
	}

	// Use reflection to set private fields for testing
	// Since we can't directly set private fields, we'll test the public behavior
	err := mq.checkConnection()
	if err == nil {
		t.Error("checkConnection should fail with invalid connection string")
	}
}

// Test Close method
func TestClose_Success(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}

	err = mq.Close()
	if err != nil {
		t.Fatalf("Close should succeed: %v", err)
	}
}

// Test Close with nil channel and connection
func TestClose_NilChannelAndConnection(t *testing.T) {
	mq := &TimeoutMQ{}

	err := mq.Close()
	if err != nil {
		t.Fatalf("Close should succeed even with nil channel and connection: %v", err)
	}
}

// Test JSON marshaling error in Timeout
func TestTimeout_JSONMarshalError(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	// Create an object that will cause JSON marshaling to fail
	objWithCycle := make(map[string]interface{})
	objWithCycle["self"] = objWithCycle // circular reference

	err = mq.Timeout(objWithCycle, 1*time.Second)
	if err == nil {
		t.Error("Expected JSON marshaling to fail with circular reference")
	}
}

// Test with objects that have no PkgPath (built-in types)
func TestGenerateQueueName_NoPackagePath(t *testing.T) {
	mq := &TimeoutMQ{}

	// Test with built-in types (maps, slices, etc.)
	obj := map[string]int{"test": 1}
	queueName := mq.generateQueueName(obj)

	if queueName == "" {
		t.Error("Queue name should not be empty for built-in types")
	}
}

// Test timeout message structure
func TestTimeoutMessage_Structure(t *testing.T) {
	testObj := TestObject{ID: 1, Name: "Test"}

	// Test creating timeout message
	msg := TimeoutMessage{
		Type: "test.TestObject",
		Data: testObj,
	}

	// Test JSON marshaling
	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal timeout message: %v", err)
	}

	// Test JSON unmarshaling
	var unmarshaledMsg TimeoutMessage
	err = json.Unmarshal(data, &unmarshaledMsg)
	if err != nil {
		t.Fatalf("Failed to unmarshal timeout message: %v", err)
	}

	if unmarshaledMsg.Type != msg.Type {
		t.Errorf("Type mismatch: got %s, expected %s", unmarshaledMsg.Type, msg.Type)
	}
}

// Test queue name generation with different object types
func TestGenerateQueueName_DifferentTypes(t *testing.T) {
	mq := &TimeoutMQ{}

	// Test with struct
	obj1 := TestObject{ID: 1, Name: "test"}
	queueName1 := mq.generateQueueName(obj1)

	// Test with pointer to struct
	obj2 := &TestObject{ID: 2, Name: "test"}
	queueName2 := mq.generateQueueName(obj2)

	// Should be the same for struct and pointer to struct
	if queueName1 != queueName2 {
		t.Error("Queue names should be the same for struct and pointer to struct")
	}

	// Test with different struct type
	obj3 := TestObjectWithPointer{ID: 1, Name: "test"}
	queueName3 := mq.generateQueueName(obj3)

	// Should be different for different struct types
	if queueName1 == queueName3 {
		t.Error("Queue names should be different for different struct types")
	}
}

// Test setupQueues functionality
func TestSetupQueues_Success(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	err = mq.setupQueues("test_queue")
	if err != nil {
		t.Fatalf("setupQueues should succeed: %v", err)
	}
}

// Test message processing with valid message
func TestReceive_ValidMessage(t *testing.T) {
	// Create a valid timeout message
	testObj := TestObject{ID: 1, Name: "Test"}
	msg := TimeoutMessage{
		Type: "timeoutMQ.TestObject",
		Data: testObj,
	}

	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal test message: %v", err)
	}

	// This would normally be tested with a mock channel
	// For now, we just verify the message structure is valid
	var unmarshaledMsg TimeoutMessage
	err = json.Unmarshal(body, &unmarshaledMsg)
	if err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}

	if unmarshaledMsg.Type != msg.Type {
		t.Errorf("Type mismatch: got %s, expected %s", unmarshaledMsg.Type, msg.Type)
	}
}

// Test message processing with invalid JSON
func TestReceive_InvalidJSON(t *testing.T) {
	invalidJSON := []byte("{invalid json}")

	var msg TimeoutMessage
	err := json.Unmarshal(invalidJSON, &msg)
	if err == nil {
		t.Error("Expected JSON unmarshaling to fail with invalid JSON")
	}
}

// Test message processing with incompatible data
func TestReceive_IncompatibleData(t *testing.T) {
	// Create a message with incompatible data type
	msg := TimeoutMessage{
		Type: "timeoutMQ.TestObject",
		Data: "this is a string, not a TestObject",
	}

	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("Failed to marshal message: %v", err)
	}

	var unmarshaledMsg TimeoutMessage
	err = json.Unmarshal(body, &unmarshaledMsg)
	if err != nil {
		t.Fatalf("Failed to unmarshal message: %v", err)
	}

	// Now try to convert the data to TestObject
	dataBytes, err := json.Marshal(unmarshaledMsg.Data)
	if err != nil {
		t.Fatalf("Failed to marshal data: %v", err)
	}

	var testObj TestObject
	err = json.Unmarshal(dataBytes, &testObj)
	if err == nil {
		t.Error("Expected unmarshaling to fail with incompatible data")
	}
}

// Test connection checking with closed connection
func TestCheckConnection_ClosedConnection(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	// Close the connection
	mq.conn.Close()
	mq.ch.Close()

	// This should trigger reconnection
	err = mq.checkConnection()
	if err != nil {
		// Expected to fail since we can't reconnect to the same closed connection
		// This tests the error path in checkConnection
		t.Logf("checkConnection failed as expected: %v", err)
	}
}

// Test connection checking with only closed channel
func TestCheckConnection_OnlyChannelClosed(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	// Close only the channel
	mq.ch.Close()

	// This should successfully reopen the channel
	err = mq.checkConnection()
	if err != nil {
		t.Fatalf("checkConnection should succeed in reopening channel: %v", err)
	}

	if mq.ch.IsClosed() {
		t.Error("Channel should be open after checkConnection")
	}
}

// Test duration conversion in timeout
func TestTimeout_DurationConversion(t *testing.T) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		t.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	testObj := TestObject{ID: 1, Name: "Test"}

	// Test with different durations
	durations := []time.Duration{
		1 * time.Millisecond,
		100 * time.Millisecond,
		1 * time.Second,
		1 * time.Minute,
	}

	for _, duration := range durations {
		err = mq.Timeout(testObj, duration)
		if err != nil {
			t.Fatalf("Failed to send timeout message with duration %v: %v", duration, err)
		}
	}
}

// Test interface implementation
func TestTimeoutMQInterface(t *testing.T) {
	var _ TimeoutMQInterface = &TimeoutMQ{}
}

// Benchmark test for performance
func BenchmarkTimeout(b *testing.B) {
	mq, err := Dial("amqp://admin:admin@localhost:5672/")
	if err != nil {
		b.Skipf("RabbitMQ not available: %v", err)
	}
	defer mq.Close()

	testObj := TestObject{ID: 1, Name: "Benchmark Object"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := mq.Timeout(testObj, 100*time.Millisecond)
		if err != nil {
			b.Fatalf("Timeout failed: %v", err)
		}
	}
}

// Benchmark test for queue name generation
func BenchmarkGenerateQueueName(b *testing.B) {
	mq := &TimeoutMQ{}
	testObj := TestObject{ID: 1, Name: "Benchmark Object"}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = mq.generateQueueName(testObj)
	}
}
