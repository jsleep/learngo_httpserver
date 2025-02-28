package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/jsleep/learngo_httpserver/internal/auth"
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
	secret         string
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

type User struct {
	ID           uuid.UUID `json:"id"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Email        string    `json:"email"`
	Token        string    `json:"token"`
	RefreshToken string    `json:"refresh_token"`
}

func (cfg *apiConfig) addUserHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	decoder.Decode(&params)

	hashedPassword, err := auth.HashPassword(params.Password)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}
	databaseUser := database.CreateUserParams{Email: params.Email, HashedPassword: hashedPassword}

	dbUser, err := cfg.db.CreateUser(r.Context(), databaseUser)
	user := User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
	}
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	statusCode := 201
	dat, _ := json.Marshal(user)

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)
}

func (cfg *apiConfig) loginHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	decoder.Decode(&params)

	dbUser, err := cfg.db.GetUser(r.Context(), params.Email)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	err = auth.CheckPasswordHash(params.Password, dbUser.HashedPassword)
	if err != nil {
		dat := []byte(fmt.Sprintf("{error:\"%s\"}", err.Error()))
		statusCode := http.StatusUnauthorized

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
		return
	}

	user := User{
		ID:        dbUser.ID,
		CreatedAt: dbUser.CreatedAt,
		UpdatedAt: dbUser.UpdatedAt,
		Email:     dbUser.Email,
	}

	jwt_token, err := auth.MakeJWT(user.ID, cfg.secret, time.Duration(60)*time.Minute)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	user.Token = jwt_token

	refresh_token, err := auth.MakeRefreshToken()
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}
	user.RefreshToken = refresh_token

	_, err = cfg.db.CreateRefreshToken(r.Context(), database.CreateRefreshTokenParams{UserID: user.ID, Token: refresh_token, ExpiresAt: time.Now().Add(time.Duration(60*24) * time.Hour)})
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	statusCode := 200
	dat, _ := json.Marshal(user)

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)

}

type Chirp struct {
	ID        uuid.UUID `json:"id"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	Body      string    `json:"body"`
	UserID    uuid.UUID `json:"user_id"`
}

func (cfg *apiConfig) addChirpHandler(w http.ResponseWriter, r *http.Request) {
	type parameters struct {
		Body string `json:"body"`
	}

	decoder := json.NewDecoder(r.Body)
	params := parameters{}
	decoder.Decode(&params)

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}
	uuid, err := auth.ValidateJWT(token, cfg.secret)
	if err != nil {
		returnError(w, http.StatusUnauthorized, err)
		return
	}

	if len(params.Body) > 140 {
		err = errors.New("Chirp is too long")
		returnError(w, http.StatusBadRequest, err)
		return

	} else {
		params.Body = Clean(params.Body)
	}

	dbParams := database.CreateChirpParams{Body: params.Body, UserID: uuid}

	dbChirp, err := cfg.db.CreateChirp(r.Context(), dbParams)
	chirp := Chirp{
		ID:        dbChirp.ID,
		CreatedAt: dbChirp.CreatedAt,
		UpdatedAt: dbChirp.UpdatedAt,
		Body:      dbChirp.Body,
		UserID:    dbChirp.UserID,
	}

	if err != nil {
		err = errors.New("Chirp is too long")
		returnError(w, http.StatusBadRequest, err)
		return
	} else {
		statusCode := 201
		dat, _ := json.Marshal(chirp)

		w.WriteHeader(statusCode)
		w.Header().Set("Content-Type", "application/json")
		w.Write(dat)
	}

}

func (cfg *apiConfig) getChirpHandler(w http.ResponseWriter, r *http.Request) {
	chirpId, err := uuid.Parse(r.PathValue("chirpID"))
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	dbChirp, err := cfg.db.GetChirp(r.Context(), chirpId)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	chirp := Chirp{
		ID:        dbChirp.ID,
		CreatedAt: dbChirp.CreatedAt,
		UpdatedAt: dbChirp.UpdatedAt,
		Body:      dbChirp.Body,
		UserID:    dbChirp.UserID,
	}

	statusCode := 200
	dat, _ := json.Marshal(chirp)

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)

}

func (cfg *apiConfig) getChirpsHandler(w http.ResponseWriter, r *http.Request) {

	dbChirps, err := cfg.db.GetChirps(r.Context())

	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	chirps := make([]Chirp, len(dbChirps))

	for i, dbChirp := range dbChirps {
		chirps[i] = Chirp{
			ID:        dbChirp.ID,
			CreatedAt: dbChirp.CreatedAt,
			UpdatedAt: dbChirp.UpdatedAt,
			Body:      dbChirp.Body,
			UserID:    dbChirp.UserID,
		}
	}

	statusCode := 200
	dat, _ := json.Marshal(chirps)

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)

}

type TokenResponse struct {
	Token string `json:"token"`
}

func (cfg *apiConfig) refreshHandler(w http.ResponseWriter, r *http.Request) {

	token, err := auth.GetBearerToken(r.Header)
	if err != nil {
		returnError(w, http.StatusBadRequest, err)
		return
	}

	db_token, err := cfg.db.GetRefreshToken(r.Context(), token)
	if err != nil {
		returnError(w, http.StatusUnauthorized, errors.New("Refresh token not found"))
		return
	}

	if db_token.ExpiresAt.Before(time.Now()) {
		returnError(w, http.StatusUnauthorized, errors.New("Refresh token expired"))
		return
	}

	jwt_token, err := auth.MakeJWT(db_token.UserID, cfg.secret, time.Duration(60)*time.Minute)
	if err != nil {
		returnError(w, http.StatusInternalServerError, err)
		return
	}

	tokenResponse := TokenResponse{Token: jwt_token}

	statusCode := 200
	dat, _ := json.Marshal(tokenResponse)

	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)

}

func returnError(w http.ResponseWriter, statusCode int, err error) {
	dat := []byte(fmt.Sprintf("{error:\"%s\"}", err.Error()))
	w.WriteHeader(statusCode)
	w.Header().Set("Content-Type", "application/json")
	w.Write(dat)
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

	cfg := &apiConfig{db: dbQueries, platform: os.Getenv("PLATFORM"), secret: os.Getenv("SECRET")}

	fileServerHandler := http.StripPrefix("/app/", http.FileServer(http.Dir(".")))
	serve_mux.Handle("/app/", cfg.middlewareMetricsInc(fileServerHandler))
	serve_mux.HandleFunc("GET /api/healthz", healthHandler)
	serve_mux.HandleFunc("GET /admin/metrics", cfg.metricsHandler)
	serve_mux.HandleFunc("POST /admin/reset", cfg.resetHandler)
	serve_mux.HandleFunc("POST /api/users", cfg.addUserHandler)
	serve_mux.HandleFunc("POST /api/login", cfg.loginHandler)
	serve_mux.HandleFunc("POST /api/chirps", cfg.addChirpHandler)
	serve_mux.HandleFunc("GET /api/chirps", cfg.getChirpsHandler)
	serve_mux.HandleFunc("GET /api/chirps/{chirpID}", cfg.getChirpHandler)
	serve_mux.HandleFunc("POST /api/refresh", cfg.refreshHandler)
	serve_mux.HandleFunc("POST /api/revoke", cfg.revokeHandler)

	server := http.Server{Handler: serve_mux, Addr: ":8080"}

	// fmt.Println("Starting server on :8080")
	server.ListenAndServe()
}
