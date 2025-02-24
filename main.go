package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/jsleep/learngo_httpserver/internal/database"
	_ "github.com/lib/pq"
)

func helloHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Hello, World!")
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-Type", "Content-Type: text/plain; charset=utf-8")
	w.Write([]byte("OK"))
}

type apiConfig struct {
	fileserverHits atomic.Int32
	db             *database.Queries
	platform       string
}

func (cfg *apiConfig) middlewareMetricsInc(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cfg.fileserverHits.Add(1) // Increment here for **each request**.
		next.ServeHTTP(w, r)      // Pass the request to the next handler.
	})
}

func (cfg *apiConfig) metricsHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Header().Add("Content-Type", "Content-Type: text/html")
	w.Write([]byte(fmt.Sprintf(
		`<html>
			<body>
				<h1>Welcome, Chirpy Admin</h1>
				<p>Chirpy has been visited %d times!</p>
			</body>
		</html>`,
		cfg.fileserverHits.Load())))
}

func (cfg *apiConfig) resetHandler(w http.ResponseWriter, r *http.Request) {
	if cfg.platform != "dev" {
		w.WriteHeader(http.StatusForbidden)
		w.Header().Add("Content-Type", "Content-Type: text/plain; charset=utf-8")
		w.Write([]byte("Forbidden"))
		return
	}

	err := cfg.db.ClearUsers(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		w.Header().Add("Content-Type", "Content-Type: text/plain; charset=utf-8")
		w.Write([]byte("Internal Server Error"))
	}
	w.WriteHeader(http.StatusOK)
	cfg.fileserverHits.Store(0)
	w.Header().Add("Content-Type", "Content-Type: text/plain; charset=utf-8")
	w.Write([]byte("OK"))
}

func Clean(body string) string {
	bad_words := map[string]bool{"kerfuffle": true, "sharbert": true, "fornax": true}

	body_words := strings.Split(body, " ")

	for i := 0; i < len(body_words); i++ {
		word := body_words[i]
		if bad_words[strings.ToLower(word)] {
			body_words[i] = "****"
		}
	}
	return strings.Join(body_words, " ")
}

func validateChirpHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body string `json:"body"`
	}

	type response struct {
		CleanedBody string `json:"cleaned_body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	err := decoder.Decode(&params)

	resp := response{}
	statusCode := http.StatusOK
	if err != nil {
		statusCode = http.StatusBadRequest
	} else if len(params.Body) > 140 {
		statusCode = http.StatusBadRequest
	} else {
		resp.CleanedBody = Clean(params.Body)
	}
	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	dat, err := json.Marshal(resp)
	if err != nil {
		dat = []byte("{error:\"Internal Server Error\"}")
	}
	w.Write(dat)
}

type User struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Email     string    `json:"email"`
}

func (cfg *apiConfig) addUserHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email string `json:"email"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	decoder.Decode(&params)

	dbUser, err := cfg.db.CreateUser(r.Context(), params.Email)
	user := User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
	}
	if err != nil {
		dat := []byte(fmt.Sprintf("{error:\"%s\"}", err.Error()))
		statusCode := http.StatusInternalServerError

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
	} else {
		statusCode := 201
		dat, _ := json.Marshal(user)

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
	}

}

func (cfg *apiConfig) addChirpHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body   string    `json:"body"`
		UserID uuid.UUID `json:"user_id"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	decoder.Decode(&params)

	dbUser, err := cfg.db.CreateUser(r.Context(), params.Email)
	user := User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
	}
	if err != nil {
		dat := []byte(fmt.Sprintf("{error:\"%s\"}", err.Error()))
		statusCode := http.StatusInternalServerError

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
	} else {
		statusCode := 201
		dat, _ := json.Marshal(user)

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
	}

}

func main() {
	serve_mux := http.NewServeMux()
	godotenv.Load()

	dbURL := os.Getenv("DB_URL")
	db, err := sql.Open("postgres", dbURL)
	if err != nil {
		panic(err)
	}
	dbQueries := database.New(db)

	cfg := &apiConfig{db: dbQueries, platform: os.Getenv("PLATFORM")}

	handler := http.StripPrefix("/app/", http.FileServer(http.Dir(".")))
	serve_mux.Handle("/app/", cfg.middlewareMetricsInc(handler))
	serve_mux.HandleFunc("GET /api/healthz", healthHandler)
	serve_mux.HandleFunc("GET /admin/metrics", cfg.metricsHandler)
	serve_mux.HandleFunc("POST /admin/reset", cfg.resetHandler)
	serve_mux.HandleFunc("POST /api/validate_chirp", validateChirpHandler)
	serve_mux.HandleFunc("POST /api/users", cfg.addUserHandler)
	serve_mux.HandleFunc("POST /api/chirps", cfg.addChirpHandler)
	server := http.Server{Handler: serve_mux, Addr: ":8080"}

	// fmt.Println("Starting server on :8080")
	server.ListenAndServe()
}
