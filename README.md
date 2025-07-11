# TimeoutMQ

TimeoutMQ is a high-level Go library that provides a simple interface for implementing distributed timeout functionality using RabbitMQ. It allows you to easily schedule objects to be delivered after a specified timeout period in a distributed system.

## Features

- **Simple API**: Easy-to-use interface for timeout operations
- **Type Safety**: Automatic serialization/deserialization of Go structs
- **Distributed**: Works across multiple services and instances
- **Persistent**: Messages survive RabbitMQ restarts
- **Automatic Reconnection**: Handles connection failures gracefully
- **Queue Management**: Automatically creates and manages necessary queues and exchanges

## Installation

```bash
go get github.com/yourusername/timeoutMQ
```

## Prerequisites

- Go 1.23.1 or later
- RabbitMQ server running and accessible

## Quick Start

```go
package main

import (
    "fmt"
    "log"
    "time"
    "timeoutMQ"
)

type OrderTimeout struct {
    OrderID   int    `json:"order_id"`
    CustomerID string `json:"customer_id"`
    Action    string `json:"action"`
}

func main() {
    // Connect to RabbitMQ
    mq, err := timeoutMQ.Dial("amqp://guest:guest@localhost:5672/")
    if err != nil {
        log.Fatal("Failed to connect to RabbitMQ:", err)
    }
    defer mq.Close()

    // Create an object to timeout
    order := OrderTimeout{
        OrderID:    12345,
        CustomerID: "customer-abc",
        Action:     "cancel_unpaid_order",
    }

    // Schedule the object to timeout after 5 minutes
    err = mq.Timeout(order, 5*time.Minute)
    if err != nil {
        log.Fatal("Failed to schedule timeout:", err)
    }

    fmt.Println("Order timeout scheduled for 5 minutes")

    // In another part of your application (or another service),
    // listen for timed out objects
    go func() {
        objectCh, err := mq.Recieve(&OrderTimeout{})
        if err != nil {
            log.Fatal("Failed to setup receiver:", err)
        }

        for timedOutObj := range objectCh {
            order := timedOutObj.(*OrderTimeout)
            fmt.Printf("Processing timed out order: %+v\n", order)
            
            // Handle the timeout - cancel order, send notification, etc.
            handleOrderTimeout(order)
        }
    }()

    // Keep the application running
    select {}
}

func handleOrderTimeout(order *OrderTimeout) {
    // Your timeout handling logic here
    fmt.Printf("Canceling unpaid order %d for customer %s\n", 
        order.OrderID, order.CustomerID)
}
```

## API Reference

### Creating a Connection

```go
mq, err := timeoutMQ.Dial("amqp://username:password@host:port/")
```

### Scheduling a Timeout

```go
err := mq.Timeout(object, duration)
```

- `object`: Any struct that can be JSON marshaled
- `duration`: `time.Duration` specifying when the object should timeout

### Receiving Timed Out Objects

```go
objectCh, err := mq.Recieve(&YourStruct{})
```

Returns a channel that will receive objects of the specified type when they timeout.

### Closing the Connection
Technically not required if you are okay with not ending your connections gracefully
```go
err := mq.Close()
```

## Use Cases

### 1. Order Processing

```go
type OrderTimeout struct {
    OrderID   int       `json:"order_id"`
    CreatedAt time.Time `json:"created_at"`
    Action    string    `json:"action"`
}

// Schedule order cancellation after 30 minutes
order := OrderTimeout{
    OrderID:   123,
    CreatedAt: time.Now(),
    Action:    "cancel_unpaid",
}
mq.Timeout(order, 30*time.Minute)
```

### 2. Session Management

```go
type SessionTimeout struct {
    SessionID string `json:"session_id"`
    UserID    string `json:"user_id"`
    Action    string `json:"action"`
}

// Auto-logout user after 2 hours of inactivity
session := SessionTimeout{
    SessionID: "sess_abc123",
    UserID:    "user_456",
    Action:    "logout",
}
mq.Timeout(session, 2*time.Hour)
```

### 3. Reminder System

```go
type ReminderTimeout struct {
    ReminderID int    `json:"reminder_id"`
    UserID     string `json:"user_id"`
    Message    string `json:"message"`
    Type       string `json:"type"`
}

// Send reminder after 1 day
reminder := ReminderTimeout{
    ReminderID: 789,
    UserID:     "user_123",
    Message:    "Don't forget to complete your profile!",
    Type:       "profile_completion",
}
mq.Timeout(reminder, 24*time.Hour)
```

## Advanced Usage

### Multiple Object Types

You can use different struct types, and they will be automatically routed to separate queues:

```go
type EmailTimeout struct {
    EmailID string `json:"email_id"`
    Action  string `json:"action"`
}

type SMSTimeout struct {
    PhoneNumber string `json:"phone_number"`
    Message     string `json:"message"`
}

// These will use different queues automatically
mq.Timeout(EmailTimeout{EmailID: "email1", Action: "retry"}, 1*time.Hour)
mq.Timeout(SMSTimeout{PhoneNumber: "+1234567890", Message: "Hello"}, 30*time.Minute)

// Separate receivers for each type
emailCh, _ := mq.Recieve(&EmailTimeout{})
smsCh, _ := mq.Recieve(&SMSTimeout{})
```

### Error Handling

```go
// The library automatically handles connection failures
err := mq.Timeout(object, duration)
if err != nil {
    log.Printf("Failed to schedule timeout: %v", err)
    // Implement retry logic or fallback mechanism
}
```

## How It Works

1. **Queue Creation**: For each object type, TimeoutMQ creates:
   - A timeout queue with TTL (time-to-live) settings
   - A dead letter exchange
   - A result queue where expired messages are delivered

2. **Message Flow**:
   - Objects are serialized to JSON and sent to the timeout queue
   - Messages expire after the specified duration
   - Expired messages are automatically moved to the result queue
   - Consumers receive and deserialize the timed-out objects

3. **Type Safety**: The library uses Go's reflection to maintain type information and ensure objects are correctly deserialized.

## Configuration

### Connection String Format

```
amqp://username:password@host:port/vhost
```

Examples:
- `amqp://guest:guest@localhost:5672/`
- `amqp://admin:admin@rabbitmq.example.com:5672/production`

### RabbitMQ Setup

The library automatically creates the necessary queues and exchanges. No manual RabbitMQ configuration is required.

## Testing

Run the tests with:

```bash
go test -v
```

Note: Tests require a running RabbitMQ instance at `amqp://admin:admin@localhost:5672/`

## License

[Your License Here]

## Contributing

[Your contribution guidelines here]