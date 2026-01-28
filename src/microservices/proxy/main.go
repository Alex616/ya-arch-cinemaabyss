package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type urlWithPercent struct {
	url     string
	percent int
}

type GradualProxy struct {
	baseURL         string
	prefix          string
	enabled         bool
	urslByDirection map[string]urlWithPercent

	counters map[string]int64
	mut      sync.Mutex
}

func NewGradualProxy(baseURL string, enabled bool, urslByDirection map[string]urlWithPercent) *GradualProxy {
	return &GradualProxy{
		baseURL:         baseURL,
		prefix:          "/api/",
		enabled:         enabled,
		urslByDirection: urslByDirection,
		counters:        make(map[string]int64, 3),
	}
}

func (g *GradualProxy) gradualTargetURL(direction string) string {
	if !g.enabled {
		return g.baseURL + g.prefix + direction
	}
	urslPercent, ok := g.urslByDirection[direction]
	if !ok {
		return g.baseURL + g.prefix + direction
	}
	count := g.incrementRequestCount(direction)
	log.Printf("Направление: %s, номер запроса: %d\n", direction, count)
	targetURL := g.calculateTargetURL(urslPercent.percent, count, g.baseURL, urslPercent.url)

	return targetURL + g.prefix + direction
}

func (g *GradualProxy) incrementRequestCount(direction string) int64 {
	g.mut.Lock()
	defer g.mut.Unlock()
	count, ok := g.counters[direction]
	if !ok {
		g.counters[direction] = 0
	}
	g.counters[direction]++
	return count + 1
}

func (g *GradualProxy) calculateTargetURL(percent int, reqCount int64, defaultUrl string, newUrl string) string {
	if percent <= 0 || newUrl == "" {
		return defaultUrl
	}
	// Нечетные запросы (1,3,5,7...) идут в новый сервис
	// Четные запросы (2,4,6,8...) идут в монолит
	if reqCount%int64(100/percent) == 0 {
		return newUrl
	}

	return defaultUrl
}

func (g *GradualProxy) proxyRequest(w http.ResponseWriter, r *http.Request, targetURL string) {
	// Парсим целевой URL для добавления query параметров
	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		http.Error(w, "Ошибка парсинга целевого URL", http.StatusInternalServerError)
		return
	}

	// Прокидываем query параметры из оригинального запроса
	if r.URL.RawQuery != "" {
		parsedURL.RawQuery = r.URL.RawQuery
	}

	finalURL := parsedURL.String()
	log.Printf("Проксирование запроса к %s\n", finalURL)

	// Создаем новый запрос с телом (для POST/PUT параметров)
	req, err := http.NewRequest(r.Method, finalURL, r.Body)
	if err != nil {
		http.Error(w, "Ошибка создания запроса", http.StatusInternalServerError)
		return
	}
	req.Header = r.Header

	client := &http.Client{
		Timeout: 10 * time.Second,
	}
	response, err := client.Do(req)
	if err != nil {
		http.Error(w, "Ошибка проксирования запроса", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()

	// Копируем все заголовки из ответа бэкенда
	for key, values := range response.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	w.WriteHeader(response.StatusCode)
	io.Copy(w, response.Body)
}

func (g *GradualProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// /api/{direction}/...
	url := strings.TrimPrefix(r.URL.Path, g.prefix)
	res := strings.SplitN(url, "/", 2)
	if len(res) == 0 {
		w.WriteHeader(http.StatusNotFound)
	}
	direction := res[0]
	postfix := ""
	if len(res) > 1 {
		postfix = res[1]
	}

	targetURL := g.gradualTargetURL(direction)
	if targetURL == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	if postfix != "" {
		targetURL = targetURL + "/" + postfix
	}
	g.proxyRequest(w, r, targetURL)

}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]bool{"status": true})
}

func main() {

	monolithURL := getEnv("MONOLITH_URL", "http://localhost:8080")
	moviesURL := getEnv("MOVIES_SERVICE_URL", "http://localhost:8001")
	eventsURL := getEnv("EVENTS_SERVICE_URL", "http://localhost:8002")

	gradualMigrationEnabled := getEnv("GRADUAL_MIGRATION", "false") == "true"
	movieMigrationPercentEnv := getEnv("MOVIES_MIGRATION_PERCENT", "50")
	movieMigrationPercent, err := strconv.Atoi(movieMigrationPercentEnv)
	if err != nil {
		log.Fatalf("Неверное значение MOVIES_MIGRATION_PERCENT: %v\n", err)
	}

	proxy := NewGradualProxy(monolithURL, gradualMigrationEnabled, map[string]urlWithPercent{
		"movies":        {url: moviesURL, percent: movieMigrationPercent},
		"events":        {url: eventsURL, percent: 100},
		"users":         {url: "", percent: 0},
		"payments":      {url: "", percent: 0},
		"subscriptions": {url: "", percent: 0},
	})

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.Handle("/", proxy)
	// mux.HandleFunc("/api/{direction}/", proxy.handleAPI)
	port := getEnv("PORT", "8000")

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}
	// Запуск сервера в горутине
	go func() {
		log.Printf("Proxy запущен на порту %s\n", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Ошибка запуска сервера: %v\n", err)
		}
	}()

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit
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

func gradualMigration(enabled bool, percent int, reqCount int64, defaultUrl string, newUrl string) string {
	if !enabled {
		return defaultUrl
	}

	// Нечетные запросы (1,3,5,7...) идут в новый сервис
	// Четные запросы (2,4,6,8...) идут в монолит
	if reqCount%int64(100/percent) == 0 {
		return newUrl
	}

	return defaultUrl
}
