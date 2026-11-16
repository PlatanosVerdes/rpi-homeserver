// Package httpx holds what the Re-encode and Add audio handlers share.
package httpx

import (
	"encoding/json"
	"net/http"
)

// HistoryShown is how many finished items each view of the page lists.
const HistoryShown = 15

func WriteJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(payload)
}

// PostOnly answers 405 to anything but a POST, and says whether the handler should go on.
func PostOnly(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return false
	}
	return true
}
