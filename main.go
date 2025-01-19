package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Config struct {
	ServerHost string `json:"server_host"`
	ServerPort string `json:"server_port"`
	FileDir    string `json:"file_dir"`
	UserFile   string `json:"user_file"`
}

type User struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type UserStore struct {
	mu    sync.RWMutex
	Users []User `json:"users"`
}

var (
	config    Config
	userStore UserStore
	templates = template.Must(template.ParseFiles("templates/main.html"))
)

func main() {
	// Load config
	configFile, err := os.Open("config.json")
	if err != nil {
		log.Fatalf("Failed to load config.json: %v", err)
	}
	defer configFile.Close()

	if err := json.NewDecoder(configFile).Decode(&config); err != nil {
		log.Fatalf("Failed to parse config.json: %v", err)
	}

	// Load users from file
	loadUsers()

	// Ensure file directory exists
	if _, err := os.Stat(config.FileDir); os.IsNotExist(err) {
		if err := os.Mkdir(config.FileDir, os.ModePerm); err != nil {
			log.Fatalf("Failed to create files directory: %v", err)
		}
	}

	// HTTP routes
	http.HandleFunc("/", mainPageHandler)
	http.HandleFunc("/upload", authMiddleware(uploadHandler))
	http.HandleFunc("/files", authMiddleware(fileListHandler))
	http.HandleFunc("/delete", authMiddleware(deleteHandler))
	http.HandleFunc("/download", authMiddleware(downloadHandler))
	http.HandleFunc("/logout", logoutHandler)
	http.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir("static"))))

	// Start server
	address := fmt.Sprintf("%s:%s", config.ServerHost, config.ServerPort)
	log.Printf("Starting server on %s", address)
	if err := http.ListenAndServe(address, nil); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

// Load users from JSON file
func loadUsers() {
	file, err := os.Open(config.UserFile)
	if err != nil {
		log.Fatalf("Failed to open user file: %v", err)
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&userStore); err != nil {
		log.Fatalf("Failed to parse user file: %v", err)
	}
}

// Validate credentials
func validateCredentials(username, password string) bool {
	userStore.mu.RLock()
	defer userStore.mu.RUnlock()

	for _, user := range userStore.Users {
		if user.Username == username && user.Password == password {
			return true
		}
	}
	return false
}

// Authentication middleware
func authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("auth")
		if err != nil {
			log.Printf("Unauthorized access attempt: %v", r.RemoteAddr)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		authParts := strings.Split(cookie.Value, ":")
		if len(authParts) != 2 || !validateCredentials(authParts[0], authParts[1]) {
			log.Printf("Invalid cookie or credentials: %v", r.RemoteAddr)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}

		// Pass to the next handler if authorized
		next(w, r)
	}
}

// Main page handler (login + dashboard)
func mainPageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		username := r.FormValue("username")
		password := r.FormValue("password")

		if validateCredentials(username, password) {
			http.SetCookie(w, &http.Cookie{
				Name:  "auth",
				Value: username + ":" + password,
				Path:  "/",
			})
		} else {
			http.Error(w, "Invalid credentials", http.StatusUnauthorized)
			return
		}
	}

	cookie, err := r.Cookie("auth")
	var isAuthorized bool
	if err == nil {
		authParts := strings.Split(cookie.Value, ":")
		if len(authParts) != 2 {
			isAuthorized = false
		} else {
			isAuthorized = validateCredentials(authParts[0], authParts[1])
		}
	}

	files, _ := os.ReadDir(config.FileDir)
	fileNames := []string{}
	for _, file := range files {
		fileNames = append(fileNames, file.Name())
	}

	data := struct {
		IsAuthorized bool
		Files        []string
	}{
		IsAuthorized: isAuthorized,
		Files:        fileNames,
	}

	err = templates.ExecuteTemplate(w, "main.html", data)
	if err != nil {
		http.Error(w, "Failed to render page", http.StatusInternalServerError)
	}
}

// Upload handler
func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Failed to read file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	filePath := filepath.Join(config.FileDir, header.Filename)
	out, err := os.Create(filePath)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	defer out.Close()

	if _, err := io.Copy(out, file); err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

// File listing handler
func fileListHandler(w http.ResponseWriter, r *http.Request) {
	// Logic for listing files
}

// File delete handler
func deleteHandler(w http.ResponseWriter, r *http.Request) {
	// Logic for deleting files
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	fileName := r.FormValue("filename")
	filePath := filepath.Join(config.FileDir, fileName)

	// Попытка удалить файл
	if err := os.Remove(filePath); err != nil {
		http.Error(w, "Failed to delete file", http.StatusInternalServerError)
		return
	}

	// Перенаправление на главную страницу после успешного удаления
	http.Redirect(w, r, "/", http.StatusFound)
}

// File download handler
func downloadHandler(w http.ResponseWriter, r *http.Request) {
	fileName := r.URL.Query().Get("filename")
	filePath := filepath.Join(config.FileDir, fileName)

	file, err := os.Open(filePath)
	if err != nil {
		http.Error(w, "File not found", http.StatusNotFound)
		return
	}
	defer file.Close()

	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", fileName))
	w.Header().Set("Content-Type", "application/octet-stream")
	if _, err := io.Copy(w, file); err != nil {
		http.Error(w, "Failed to download file", http.StatusInternalServerError)
	}
}
func logoutHandler(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:   "auth",
		Value:  "",
		Path:   "/",
		MaxAge: -1,
	})
	http.Redirect(w, r, "/", http.StatusFound)
}
