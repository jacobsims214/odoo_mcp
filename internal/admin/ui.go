package admin

import (
	"embed"
	"net/http"
)

//go:embed admin.html
var adminFS embed.FS

// AdminPage serves the admin HTML page.
func AdminPage(w http.ResponseWriter, r *http.Request) {
	data, err := adminFS.ReadFile("admin.html")
	if err != nil {
		http.Error(w, "admin page not found", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(data)
}
