package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/segmentio/kafka-go"
)

const (
	usersTopic    = "user-events"
	paymentsTopic = "payments-events"
	moviesTopic   = "movies-events"
)

type Message struct {
	Message string `json:"message"`
}

type App struct {
	kafakaWriter *kafka.Writer
}

func NewServer(kafakaWriter *kafka.Writer) *App {
	return &App{
		kafakaWriter: kafakaWriter,
	}
}

func (a *App) HealthHandler(w http.ResponseWriter, r *http.Request) {

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"status": true})
}

func (a *App) UsersHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	value := "unknown"
	switch r.Method {
	case http.MethodPost:
		value = "create"
	case http.MethodGet:
		value = "get"
	}

	msg := Message{
		Message: value,
	}
	msgBytes, _ := json.Marshal(msg)

	message := kafka.Message{
		Topic: usersTopic,
		Value: msgBytes,
	}
	a.kafakaWriter.WriteMessages(ctx, message)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (a *App) PaymentsHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	value := "unknown"
	switch r.Method {
	case http.MethodPost:
		value = "create"
	case http.MethodGet:
		value = "get"
	}
	msg := Message{
		Message: value,
	}
	msgBytes, _ := json.Marshal(msg)

	message := kafka.Message{
		Topic: paymentsTopic,
		Value: msgBytes,
	}
	a.kafakaWriter.WriteMessages(ctx, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (a *App) MoviesHandler(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	value := "unknown"
	switch r.Method {
	case http.MethodPost:
		value = "create"
	case http.MethodGet:
		value = "get"
	}

	msg := Message{
		Message: value,
	}
	msgBytes, _ := json.Marshal(msg)
	message := kafka.Message{
		Topic: moviesTopic,
		Value: msgBytes,
	}
	a.kafakaWriter.WriteMessages(ctx, message)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]string{"status": "success"})
}

func (a *App) Close() error {
	return a.kafakaWriter.Close()
}

type BusConsumer struct {
	kafkaReader *kafka.Reader
}

func NewBusConsumer(brokers []string, topic, groupID string) *BusConsumer {

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		GroupID:  groupID,
		Topic:    topic,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})

	return &BusConsumer{
		kafkaReader: reader,
	}
}

func (bc *BusConsumer) Close() error {
	return bc.kafkaReader.Close()
}

func (bc *BusConsumer) StartConsuming(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				log.Println("Остановка потребителя Kafka...")
				return
			default:
			}
			msg, err := bc.kafkaReader.ReadMessage(ctx)
			if err != nil {
				log.Printf("Ошибка чтения сообщения: %v\n", err)
				if err == context.Canceled {
					return
				}
				continue
			}
			var message Message
			if err := json.Unmarshal(msg.Value, &message); err != nil {
				log.Printf("Ошибка разбора сообщения: %v\n", err)
				bc.kafkaReader.CommitMessages(ctx, msg)
				continue
			}
			log.Printf("Получено сообщение из топика %s: %s\n", msg.Topic, message.Message)
			if err := bc.kafkaReader.CommitMessages(ctx, msg); err != nil {
				log.Printf("Ошибка подтверждения сообщения: %v\n", err)
			}
		}
	}()
}

func main() {

	kafakaURL := getEnv("KAFKA_BROKERS", "kafka:9092")

	kafkaWriter := &kafka.Writer{
		Addr:     kafka.TCP(kafakaURL),
		Balancer: &kafka.LeastBytes{},
	}

	app := NewServer(kafkaWriter)
	defer app.Close()
	consumerCtx, cancel := context.WithCancel(context.Background())
	mux := http.NewServeMux()
	mux.HandleFunc("/api/events/health", app.HealthHandler)
	mux.HandleFunc("/api/events/user", app.UsersHandler)
	userConsumer := NewBusConsumer([]string{kafakaURL}, usersTopic, "user-service-group")
	defer userConsumer.Close()
	userConsumer.StartConsuming(consumerCtx)
	mux.HandleFunc("/api/events/payment", app.PaymentsHandler)
	paymentConsumer := NewBusConsumer([]string{kafakaURL}, paymentsTopic, "payment-service-group")
	defer paymentConsumer.Close()
	paymentConsumer.StartConsuming(consumerCtx)
	mux.HandleFunc("/api/events/movie", app.MoviesHandler)
	movieConsumer := NewBusConsumer([]string{kafakaURL}, moviesTopic, "movie-service-group")
	defer movieConsumer.Close()
	movieConsumer.StartConsuming(consumerCtx)

	port := getEnv("PORT", "8000")
	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	// Запуск сервера в горутине
	go func() {
		log.Printf("Events сервис запущен на порту %s\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска сервера: %v\n", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
	cancel()
	log.Println("Остановка сервера...")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		log.Fatalf("Принудительная остановка сервера: %v\n", err)
	}

	log.Println("Сервер остановлен корректно")

}

func getEnv(key, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}
