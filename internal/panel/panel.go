package panel

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/http/cookiejar"
	"regexp"
	"strings"
	"time"

	"vpn-bot/internal/config"
	"vpn-bot/internal/db"
)

var (
	client    *http.Client
	csrfToken string
)

func InitHTTPClient() {
	jar, _ := cookiejar.New(nil)
	client = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		Jar: jar,
	}
}

// addAuthHeaders adds the required authentication and request headers to every request.
// In Bearer token mode no cookies or CSRF token are needed.
func addAuthHeaders(req *http.Request) {
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", config.Cfg.PanelURL+"/")
	if config.Cfg.PanelAPIToken != "" {
		req.Header.Set("Authorization", "Bearer "+config.Cfg.PanelAPIToken)
	} else if csrfToken != "" {
		req.Header.Set("X-CSRF-Token", csrfToken)
	}
}

// Login authenticates with the panel.
// In Bearer token mode it is a no-op. Otherwise it performs a cookie+CSRF login.
func Login() error {
	if config.Cfg.PanelAPIToken != "" {
		return nil // Bearer token mode — no login needed
	}

	// Step 1: GET /login to pick up the session cookie and CSRF token.
	req, err := http.NewRequest("GET", config.Cfg.PanelURL+"/login", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Referer", config.Cfg.PanelURL+"/")

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("ошибка GET /login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Extract CSRF token from <meta name="csrf-token" content="TOKEN">.
	re := regexp.MustCompile(`<meta\s+name=["']csrf-token["']\s+content=["']([^"']+)["']`)
	if m := re.FindSubmatch(body); len(m) > 1 {
		csrfToken = string(m[1])
	}

	// Step 2: POST /login with credentials and CSRF header.
	data := fmt.Sprintf("username=%s&password=%s", config.Cfg.PanelUsername, config.Cfg.PanelPassword)
	req2, err := http.NewRequest("POST", config.Cfg.PanelURL+"/login", strings.NewReader(data))
	if err != nil {
		return err
	}
	req2.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req2.Header.Set("Accept", "application/json")
	req2.Header.Set("X-Requested-With", "XMLHttpRequest")
	req2.Header.Set("Referer", config.Cfg.PanelURL+"/login")
	if csrfToken != "" {
		req2.Header.Set("X-CSRF-Token", csrfToken)
	}

	resp2, err := client.Do(req2)
	if err != nil {
		return err
	}
	defer resp2.Body.Close()

	var result map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&result); err != nil {
		return err
	}
	if success, ok := result["success"].(bool); !success || !ok {
		return fmt.Errorf("ошибка авторизации: %v", result["msg"])
	}

	log.Printf("✅ Авторизация в панели успешна")
	return nil
}

func getRequest(method string) ([]byte, error) {
	req, err := http.NewRequest("GET", config.Cfg.PanelURL+method, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	addAuthHeaders(req)
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP ошибка (%s): %v", method, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		return nil, fmt.Errorf("пустой ответ от панели (status: %d, метод: %s)", resp.StatusCode, method)
	}
	return body, nil
}

func postRequest(method string, payload interface{}) error {
	bodyBytes := []byte("{}")
	if payload != nil {
		var err error
		bodyBytes, err = json.Marshal(payload)
		if err != nil {
			return err
		}
	}

	req, err := http.NewRequest("POST", config.Cfg.PanelURL+method, bytes.NewBuffer(bodyBytes))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	addAuthHeaders(req)

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP ошибка (%s): %v", method, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		return fmt.Errorf("пустой ответ от панели (status: %d, метод: %s)", resp.StatusCode, method)
	}

	var result map[string]interface{}
	if err := json.Unmarshal(body, &result); err != nil {
		return fmt.Errorf("ошибка парсинга ответа (status: %d): %v", resp.StatusCode, err)
	}
	if success, ok := result["success"].(bool); ok && !success {
		return fmt.Errorf("ошибка API: %v", result["msg"])
	}
	return nil
}

// addClient creates a new client and attaches it to all given inbounds in one call (3X-UI v3.1.0+).
func addClient(email, comment, subID, uuid string, expiryMs int64, inboundIDs []int64) error {
	return postRequest("/panel/api/clients/add", map[string]interface{}{
		"client": map[string]interface{}{
			"id":         uuid,
			"email":      email,
			"comment":    comment,
			"subId":      subID,
			"enable":     true,
			"alterId":    0,
			"totalGB":    0,
			"expiryTime": expiryMs,
			"flow":       "",
			"tgId":       0,
			"limitIp":    0,
		},
		"inboundIds": inboundIDs,
	})
}

// updateClientByEmail updates an existing client's fields via the new email-keyed endpoint.
// Changes propagate to every inbound the client is attached to.
func updateClientByEmail(email, comment, subID, uuid string, expiryMs int64) error {
	return postRequest(
		fmt.Sprintf("/panel/api/clients/update/%s", email),
		map[string]interface{}{
			"id":         uuid,
			"email":      email,
			"comment":    comment,
			"subId":      subID,
			"enable":     true,
			"alterId":    0,
			"totalGB":    0,
			"expiryTime": expiryMs,
			"flow":       "",
			"tgId":       0,
			"limitIp":    0,
		},
	)
}

// deleteClientByEmail removes a client from every attached inbound (3X-UI v3.1.0+).
func deleteClientByEmail(email string) error {
	return postRequest(
		fmt.Sprintf("/panel/api/clients/del/%s", email),
		nil,
	)
}

// attachClientToInbound attaches an existing client (by email) to additional inbounds.
func attachClientToInbound(email string, inboundIDs []int64) error {
	return postRequest(
		fmt.Sprintf("/panel/api/clients/%s/attach", email),
		map[string]interface{}{
			"inboundIds": inboundIDs,
		},
	)
}

// AddClient adds the client to all configured inbounds using a single shared email.
// ownerID identifies the manager who created this client (0 = admin/no specific owner).
func AddClient(name string, ownerID int64) (string, error) {
	if err := Login(); err != nil {
		return "", fmt.Errorf("ошибка авторизации: %v", err)
	}

	inbounds := config.Cfg.Inbounds
	if len(inbounds) == 0 {
		return "", fmt.Errorf("не настроены inbound в конфиге")
	}

	subscription := normalizeName(name)
	uuid := generateUUID()
	email := randomEmail() // one shared email for all inbounds

	var expiryMs int64
	if ownerID != 0 {
		expiryMs = db.GetOperatorExpiry(ownerID)
	}

	// Collect all inbound IDs.
	inboundIDs := make([]int64, len(inbounds))
	for i, ib := range inbounds {
		inboundIDs[i] = ib.ID
	}

	if err := addClient(email, name, subscription, uuid, expiryMs, inboundIDs); err != nil {
		return "", fmt.Errorf("ошибка добавления клиента: %v", err)
	}

	// Store the same email for every inbound in the DB.
	emails := make(map[int64]string, len(inbounds))
	for _, ib := range inbounds {
		emails[ib.ID] = email
	}

	if err := db.SaveClient(name, subscription, uuid, emails, ownerID); err != nil {
		return "", fmt.Errorf("ошибка сохранения в БД: %v", err)
	}

	return fmt.Sprintf("%s/%s", config.Cfg.SubDomain, subscription), nil
}

// DeleteClient removes a client from every inbound it has an email in,
// then deletes the database record.
func DeleteClient(id int64) (string, error) {
	emails, name, err := db.GetClientEmails(id)
	if err != nil {
		return "", fmt.Errorf("клиент не найден в базе")
	}

	if err := Login(); err != nil {
		return name, fmt.Errorf("ошибка авторизации: %v", err)
	}

	// Deduplicate emails — legacy DB may have different emails per inbound.
	seen := make(map[string]bool)
	for _, email := range emails {
		if seen[email] {
			continue
		}
		seen[email] = true
		if err := deleteClientByEmail(email); err != nil {
			log.Printf("⚠️ Ошибка удаления клиента '%s' (%s): %v", name, email, err)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return name, db.DeleteClient(id)
}

// AddExistingClientToInbound attaches an existing client to a new inbound,
// reusing their existing email.
func AddExistingClientToInbound(clientID int64, targetInboundID int64) error {
	details, err := db.GetClientDetails(clientID)
	if err != nil {
		return err
	}

	// Pick any existing email — in the new model all inbounds share one email.
	var email string
	for _, e := range details.Emails {
		email = e
		break
	}
	if email == "" {
		return fmt.Errorf("клиент не имеет email в базе данных")
	}

	if err := Login(); err != nil {
		return fmt.Errorf("ошибка авторизации: %v", err)
	}

	if err := attachClientToInbound(email, []int64{targetInboundID}); err != nil {
		return fmt.Errorf("ошибка прикрепления к inbound %d: %v", targetInboundID, err)
	}
	return db.AddClientEmail(clientID, targetInboundID, email)
}

// --- inbound list ---

// PanelInbound is a minimal representation of a 3X-UI inbound.
type PanelInbound struct {
	ID       int64
	Remark   string
	Protocol string
	Enable   bool
}

// GetInboundList fetches all inbounds from the panel.
func GetInboundList() ([]PanelInbound, error) {
	if err := Login(); err != nil {
		return nil, fmt.Errorf("ошибка авторизации: %v", err)
	}
	body, err := getRequest("/panel/api/inbounds/list")
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Obj     []struct {
			ID       int64  `json:"id"`
			Remark   string `json:"remark"`
			Protocol string `json:"protocol"`
			Enable   bool   `json:"enable"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("ошибка парсинга списка inbound: %v", err)
	}
	if !result.Success {
		return nil, fmt.Errorf("API вернул ошибку при получении inbound")
	}
	var out []PanelInbound
	for _, o := range result.Obj {
		out = append(out, PanelInbound{ID: o.ID, Remark: o.Remark, Protocol: o.Protocol, Enable: o.Enable})
	}
	return out, nil
}

// --- import ---

// ImportResult summarises one sync run from the panel.
type ImportResult struct {
	Imported int
	Skipped  int
}

type inboundClient struct {
	UUID    string
	Email   string
	Comment string
	SubID   string
}

// getInboundClients fetches all clients from one inbound (with non-empty subId).
func getInboundClients(inboundID int64) ([]inboundClient, error) {
	body, err := getRequest(fmt.Sprintf("/panel/api/inbounds/get/%d", inboundID))
	if err != nil {
		return nil, err
	}

	var envelope struct {
		Success bool `json:"success"`
		Obj     struct {
			Settings string `json:"settings"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return nil, fmt.Errorf("ошибка парсинга inbound %d: %v", inboundID, err)
	}
	if !envelope.Success {
		return nil, fmt.Errorf("API вернул ошибку для inbound %d", inboundID)
	}

	var settings struct {
		Clients []struct {
			UUID    string `json:"id"`
			Email   string `json:"email"`
			Comment string `json:"comment"`
			SubID   string `json:"subId"`
		} `json:"clients"`
	}
	if err := json.Unmarshal([]byte(envelope.Obj.Settings), &settings); err != nil {
		return nil, fmt.Errorf("ошибка парсинга settings inbound %d: %v", inboundID, err)
	}

	var out []inboundClient
	for _, c := range settings.Clients {
		if c.SubID != "" {
			out = append(out, inboundClient{UUID: c.UUID, Email: c.Email, Comment: c.Comment, SubID: c.SubID})
		}
	}
	return out, nil
}

// ImportClientsFromPanel scans the specified inbounds and imports clients into the bot DB.
func ImportClientsFromPanel(inboundIDs []int64) (ImportResult, error) {
	if len(inboundIDs) == 0 {
		return ImportResult{}, fmt.Errorf("не выбраны inbound для импорта")
	}
	if err := Login(); err != nil {
		return ImportResult{}, fmt.Errorf("ошибка авторизации: %v", err)
	}

	type entry struct {
		comment string
		uuid    string
		emails  map[int64]string
	}
	bySubID := make(map[string]*entry)

	for _, ibID := range inboundIDs {
		clients, err := getInboundClients(ibID)
		if err != nil {
			return ImportResult{}, fmt.Errorf("ошибка получения inbound %d: %v", ibID, err)
		}
		for _, c := range clients {
			e, ok := bySubID[c.SubID]
			if !ok {
				e = &entry{comment: c.Comment, emails: make(map[int64]string)}
				bySubID[c.SubID] = e
			}
			e.emails[ibID] = c.Email
			if e.comment == "" {
				e.comment = c.Comment
			}
			if e.uuid == "" {
				e.uuid = c.UUID
			}
		}
	}

	var res ImportResult
	for subID, e := range bySubID {
		if db.ClientExistsBySubscription(subID) {
			res.Skipped++
			continue
		}
		comment := e.comment
		if comment == "" {
			comment = subID
		}
		if err := db.SaveClient(comment, subID, e.uuid, e.emails, 0); err != nil {
			log.Printf("⚠️ Ошибка импорта клиента subId=%s: %v", subID, err)
			continue
		}
		res.Imported++
	}
	return res, nil
}

// DeleteManagerClients removes all clients of a manager from 3X-UI and from the database.
// Returns the number of clients deleted.
func DeleteManagerClients(managerID int64) (int, error) {
	clients, err := db.GetClientsByOwner(managerID)
	if err != nil {
		return 0, err
	}
	if len(clients) == 0 {
		return 0, nil
	}
	if err := Login(); err != nil {
		return 0, fmt.Errorf("ошибка авторизации: %v", err)
	}
	count := 0
	for _, c := range clients {
		// Deduplicate emails — legacy DB may have different emails per inbound.
		seen := make(map[string]bool)
		for _, email := range c.Emails {
			if seen[email] {
				continue
			}
			seen[email] = true
			if err := deleteClientByEmail(email); err != nil {
				log.Printf("⚠️ Ошибка удаления клиента '%s' (%s): %v", c.Comment, email, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		db.DeleteClient(c.ID)
		count++
	}
	return count, nil
}

// UpdateManagerClientsExpiry updates expiryTime in 3X-UI for all clients of a manager.
// Returns the count of clients processed.
func UpdateManagerClientsExpiry(managerID int64, expiryMs int64) (int, error) {
	clients, err := db.GetClientsByOwner(managerID)
	if err != nil {
		return 0, err
	}
	if len(clients) == 0 {
		return 0, nil
	}
	if err := Login(); err != nil {
		return 0, fmt.Errorf("ошибка авторизации: %v", err)
	}
	count := 0
	for _, c := range clients {
		// Deduplicate emails — legacy DB may have different emails per inbound.
		// In the new model all inbounds share one email, so this loops once.
		seen := make(map[string]bool)
		for _, email := range c.Emails {
			if seen[email] {
				continue
			}
			seen[email] = true
			if err := updateClientByEmail(email, c.Comment, c.Subscription, c.UUID, expiryMs); err != nil {
				log.Printf("⚠️ Ошибка обновления expiry клиента '%s' (%s): %v", c.Comment, email, err)
			}
			time.Sleep(100 * time.Millisecond)
		}
		count++
	}
	return count, nil
}

// --- helpers ---

func normalizeName(name string) string {
	cyrillic := map[rune]string{
		'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d",
		'е': "e", 'ё': "yo", 'ж': "zh", 'з': "z", 'и': "i",
		'й': "y", 'к': "k", 'л': "l", 'м': "m", 'н': "n",
		'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t",
		'у': "u", 'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch",
		'ш': "sh", 'щ': "shch", 'ъ': "", 'ы': "y", 'ь': "",
		'э': "e", 'ю': "yu", 'я': "ya",
		'А': "A", 'Б': "B", 'В': "V", 'Г': "G", 'Д': "D",
		'Е': "E", 'Ё': "Yo", 'Ж': "Zh", 'З': "Z", 'И': "I",
		'Й': "Y", 'К': "K", 'Л': "L", 'М': "M", 'Н': "N",
		'О': "O", 'П': "P", 'Р': "R", 'С': "S", 'Т': "T",
		'У': "U", 'Ф': "F", 'Х': "Kh", 'Ц': "Ts", 'Ч': "Ch",
		'Ш': "Sh", 'Щ': "Shch", 'Ъ': "", 'Ы': "Y", 'Ь': "",
		'Э': "E", 'Ю': "Yu", 'Я': "Ya",
	}

	var buf strings.Builder
	for _, r := range name {
		if repl, ok := cyrillic[r]; ok {
			buf.WriteString(repl)
		} else {
			buf.WriteRune(r)
		}
	}

	s := strings.ToLower(strings.TrimSpace(buf.String()))
	s = strings.NewReplacer(" ", "_", "-", "_").Replace(s)

	var clean strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			clean.WriteRune(r)
		}
	}

	out := clean.String()
	for strings.Contains(out, "__") {
		out = strings.ReplaceAll(out, "__", "_")
	}
	return strings.Trim(out, "_")
}

func randomEmail() string {
	const charset = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 8+rand.Intn(3))
	for i := range b {
		b[i] = charset[rand.Intn(len(charset))]
	}
	return string(b)
}

func generateUUID() string {
	b := make([]byte, 16)
	rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}
