package main

import (
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
	"os"
)

type NetworkSampleRequest struct {
	EquipoID     string       `json:"equipo_id"`
	UbicacionID  int          `json:"ubicacion_id"`
	Timestamp    string       `json:"timestamp"`
	AgentVersion string       `json:"agent_version"`
	Hostname     string       `json:"hostname"`
	Network      NetworkInfo  `json:"network"`
	Tests        Connectivity `json:"tests"`
}

type NetworkInfo struct {
	TipoConexion string  `json:"tipo_conexion"`
	NombreIF     string  `json:"nombre_interfaz"`
	SSID         *string `json:"ssid"`
	BSSID        *string `json:"bssid"`
	RSSI         *int    `json:"rssi"`
	Calidad      *int    `json:"calidad_senal"`
	IPLocal      *string `json:"ip_local"`
	Gateway      *string `json:"gateway"`
}

type Connectivity struct {
	LatGatewayMS    *int     `json:"latencia_gateway_ms"`
	LatServidorMS   *int     `json:"latencia_servidor_local_ms"`
	LatInternetMS   *int     `json:"latencia_internet_ms"`
	PerdGatewayPct  *float64 `json:"perdida_gateway_pct"`
	PerdServidorPct *float64 `json:"perdida_servidor_local_pct"`
	PerdInternetPct *float64 `json:"perdida_internet_pct"`
}

type APIResponse struct {
	OK         bool   `json:"ok"`
	Message    string `json:"message"`
	ServerTime string `json:"server_time,omitempty"`
	Estado     string `json:"estado_general,omitempty"`
	ScoreSalud *int   `json:"score_salud,omitempty"`
	ErrorCode  string `json:"error,omitempty"`
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", handlePing)
	mux.HandleFunc("/api/v1/network-samples", handleNetworkSamples)

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           loggingMiddleware(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
	}

	log.Println("Escuchando en http://127.0.0.1:8080")
	log.Fatal(server.ListenAndServe())
}

func handlePing(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, APIResponse{
		OK:         true,
		Message:    "pong",
		ServerTime: time.Now().Format(time.RFC3339),
	})
}

func handleNetworkSamples(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, APIResponse{
			OK:        false,
			Message:   "method not allowed",
			ErrorCode: "method_not_allowed",
		})
		return
	}

	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if authHeader == "" {
		writeJSON(w, http.StatusUnauthorized, APIResponse{
			OK:        false,
			Message:   "missing Authorization header",
			ErrorCode: "unauthorized",
		})
		return
	}
	if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		writeJSON(w, http.StatusUnauthorized, APIResponse{
			OK:        false,
			Message:   "invalid Authorization header",
			ErrorCode: "unauthorized",
		})
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			OK:        false,
			Message:   "cannot read request body",
			ErrorCode: "invalid_body",
		})
		return
	}
	defer r.Body.Close()

	var req NetworkSampleRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, APIResponse{
			OK:        false,
			Message:   "invalid JSON payload",
			ErrorCode: "invalid_json",
		})
		return
	}

	if err := validateRequest(req); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, APIResponse{
			OK:        false,
			Message:   err.Error(),
			ErrorCode: "validation_error",
		})
		return
	}

	score := computeScore(req)
	estado := computeStatus(score)

	log.Printf(
		"Sample recibido: equipo_id=%s ubicacion_id=%d tipo=%s if=%s ip=%s gateway=%s ssid=%s rssi=%s lat_int=%s loss_int=%s score=%d estado=%s",
		req.EquipoID,
		req.UbicacionID,
		req.Network.TipoConexion,
		emptyIfBlank(req.Network.NombreIF),
		strPtr(req.Network.IPLocal),
		strPtr(req.Network.Gateway),
		strPtr(req.Network.SSID),
		intPtr(req.Network.RSSI),
		intPtr(req.Tests.LatInternetMS),
		floatPtr(req.Tests.PerdInternetPct),
		score,
		estado,
	)

	writeJSON(w, http.StatusCreated, APIResponse{
		OK:         true,
		Message:    "sample received",
		ServerTime: time.Now().Format(time.RFC3339),
		Estado:     estado,
		ScoreSalud: &score,
	})
}

func validateRequest(req NetworkSampleRequest) error {
	if strings.TrimSpace(req.EquipoID) == "" {
		return errors.New("equipo_id is required")
	}
	if req.UbicacionID <= 0 {
		return errors.New("ubicacion_id must be greater than 0")
	}
	if strings.TrimSpace(req.Timestamp) == "" {
		return errors.New("timestamp is required")
	}
	if _, err := time.Parse(time.RFC3339, req.Timestamp); err != nil {
		return errors.New("timestamp must be valid RFC3339")
	}
	if strings.TrimSpace(req.AgentVersion) == "" {
		return errors.New("agent_version is required")
	}
	if strings.TrimSpace(req.Hostname) == "" {
		return errors.New("hostname is required")
	}
	if req.Network.TipoConexion != "wifi" && req.Network.TipoConexion != "ethernet" {
		return errors.New("network.tipo_conexion must be wifi or ethernet")
	}

	if req.Network.Calidad != nil && (*req.Network.Calidad < 0 || *req.Network.Calidad > 100) {
		return errors.New("network.calidad_senal must be between 0 and 100")
	}
	if req.Tests.LatGatewayMS != nil && *req.Tests.LatGatewayMS < 0 {
		return errors.New("latencia_gateway_ms must be >= 0")
	}
	if req.Tests.LatServidorMS != nil && *req.Tests.LatServidorMS < 0 {
		return errors.New("latencia_servidor_local_ms must be >= 0")
	}
	if req.Tests.LatInternetMS != nil && *req.Tests.LatInternetMS < 0 {
		return errors.New("latencia_internet_ms must be >= 0")
	}
	if req.Tests.PerdGatewayPct != nil && (*req.Tests.PerdGatewayPct < 0 || *req.Tests.PerdGatewayPct > 100) {
		return errors.New("perdida_gateway_pct must be between 0 and 100")
	}
	if req.Tests.PerdServidorPct != nil && (*req.Tests.PerdServidorPct < 0 || *req.Tests.PerdServidorPct > 100) {
		return errors.New("perdida_servidor_local_pct must be between 0 and 100")
	}
	if req.Tests.PerdInternetPct != nil && (*req.Tests.PerdInternetPct < 0 || *req.Tests.PerdInternetPct > 100) {
		return errors.New("perdida_internet_pct must be between 0 and 100")
	}

	return nil
}

func computeScore(req NetworkSampleRequest) int {
	scoreSenal := 100
	if req.Network.TipoConexion == "wifi" {
		scoreSenal = scoreFromRSSI(req.Network.RSSI)
	}

	scoreLatencia := scoreFromLatency(req.Tests.LatServidorMS)
	scorePerdida := scoreFromLoss(req.Tests.PerdInternetPct)

	score := int(0.35*float64(scoreSenal) + 0.35*float64(scoreLatencia) + 0.30*float64(scorePerdida))
	if score < 0 {
		score = 0
	}
	if score > 100 {
		score = 100
	}
	return score
}

func computeStatus(score int) string {
	switch {
	case score >= 85:
		return "excelente"
	case score >= 70:
		return "buena"
	case score >= 50:
		return "regular"
	default:
		return "mala"
	}
}

func scoreFromRSSI(rssi *int) int {
	if rssi == nil {
		return 20
	}
	switch {
	case *rssi >= -60:
		return 100
	case *rssi >= -67:
		return 85
	case *rssi >= -75:
		return 65
	case *rssi >= -82:
		return 40
	default:
		return 20
	}
}

func scoreFromLatency(lat *int) int {
	if lat == nil {
		return 15
	}
	switch {
	case *lat <= 5:
		return 100
	case *lat <= 15:
		return 85
	case *lat <= 30:
		return 60
	case *lat <= 60:
		return 35
	default:
		return 15
	}
}

func scoreFromLoss(loss *float64) int {
	if loss == nil {
		return 10
	}
	switch {
	case *loss == 0:
		return 100
	case *loss <= 2:
		return 85
	case *loss <= 5:
		return 65
	case *loss <= 10:
		return 35
	default:
		return 10
	}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("error escribiendo JSON: %v", err)
	}
}

func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}

func strPtr(v *string) string {
	if v == nil {
		return "null"
	}
	return *v
}

func intPtr(v *int) string {
	if v == nil {
		return "null"
	}
	return itoa(*v)
}

func floatPtr(v *float64) string {
	if v == nil {
		return "null"
	}
	return trimFloat(*v)
}

func emptyIfBlank(s string) string {
	if strings.TrimSpace(s) == "" {
		return "null"
	}
	return s
}

func itoa(v int) string {
	return strconv.Itoa(v)
}

func trimFloat(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}
