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
	"strings"
	"time"

	"vpn-bot/internal/config"
	"vpn-bot/internal/db"
)

var (
	client  *http.Client
	cookies []*http.Cookie
)

func InitHTTPClient() {
	client = &http.Client{
		Timeout: 15 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
}

func Login() error {
	data := fmt.Sprintf("username=%s&password=%s", config.Cfg.PanelUsername, config.Cfg.PanelPassword)
	req, _ := http.NewRequest("POST", config.Cfg.PanelURL+"/login", strings.NewReader(data))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	cookies = resp.Cookies()

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return err
	}
	if success, ok := result["success"].(bool); !success || !ok {
		return fmt.Errorf("ошибка авторизации: %v", result["msg"])
	}

	log.Printf("✅ Авторизация в панели успешна (cookies: %d)", len(cookies))
	return nil
}

func getRequest(method string) ([]byte, error) {
	req, err := http.NewRequest("GET", config.Cfg.PanelURL+method, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	for _, c := range cookies {
		req.AddCookie(c)
	}
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
	for _, c := range cookies {
		req.AddCookie(c)
	}

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

func addClientToInbound(inboundID int64, email, comment, subID, uuid string, expiryMs int64) error {
	settingsJSON, _ := json.Marshal(map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id": uuid, "alterId": 0, "email": email,
				"comment": comment, "subId": subID,
				"enable": true, "totalGB": 0, "expiryTime": expiryMs,
				"flow": "", "tgId": "", "limitIp": 0,
			},
		},
	})
	return postRequest("/panel/api/inbounds/addClient", map[string]interface{}{
		"id":       inboundID,
		"settings": string(settingsJSON),
	})
}

func updateClientInInbound(inboundID int64, email, comment, subID, uuid string, expiryMs int64) error {
	settingsJSON, _ := json.Marshal(map[string]interface{}{
		"clients": []map[string]interface{}{
			{
				"id": uuid, "alterId": 0, "email": email,
				"comment": comment, "subId": subID,
				"enable": true, "totalGB": 0, "expiryTime": expiryMs,
				"flow": "", "tgId": "", "limitIp": 0,
			},
		},
	})
	return postRequest(
		fmt.Sprintf("/panel/api/inbounds/updateClient/%s", uuid),
		map[string]interface{}{
			"id":       inboundID,
			"settings": string(settingsJSON),
		},
	)
}

func deleteClientByEmail(inboundID int64, email string) error {
	return postRequest(
		fmt.Sprintf("/panel/api/inbounds/%d/delClientByEmail/%s", inboundID, email),
		nil,
	)
}

// AddClient adds the client to all configured inbounds, all sharing one subId and UUID.
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
	emails := make(map[int64]string, len(inbounds))

	var expiryMs int64
	if ownerID != 0 {
		expiryMs = db.GetOperatorExpiry(ownerID)
	}

	for i, ib := range inbounds {
		email := randomEmail()
		if err := addClientToInbound(ib.ID, email, name, subscription, uuid, expiryMs); err != nil {
			return "", fmt.Errorf("ошибка добавления в inbound %d (%s): %v", ib.ID, ib.Label, err)
		}
		emails[ib.ID] = email
		if i < len(inbounds)-1 {
			time.Sleep(200 * time.Millisecond)
		}
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

	first := true
	for inboundID, email := range emails {
		if !first {
			time.Sleep(200 * time.Millisecond)
		}
		if err := deleteClientByEmail(inboundID, email); err != nil {
			log.Printf("⚠️ Ошибка удаления из inbound %d: %v", inboundID, err)
		}
		first = false
	}

	return name, db.DeleteClient(id)
}

// AddExistingClientToInbound adds an existing client to a new inbound,
// reusing their subscription and UUID.
func AddExistingClientToInbound(clientID int64, targetInboundID int64) error {
	details, err := db.GetClientDetails(clientID)
	if err != nil {
		return err
	}

	if err := Login(); err != nil {
		return fmt.Errorf("ошибка авторизации: %v", err)
	}

	uuid := details.UUID
	if uuid == "" {
		// Try to find UUID from an existing inbound via panel API.
		for ibID := range details.Emails {
			clients, err := getInboundClients(ibID)
			if err != nil {
				continue
			}
			for _, c := range clients {
				if c.SubID == details.Subscription && c.UUID != "" {
					uuid = c.UUID
					db.SetClientUUID(clientID, uuid)
					break
				}
			}
			if uuid != "" {
				break
			}
		}
	}
	if uuid == "" {
		uuid = generateUUID()
		db.SetClientUUID(clientID, uuid)
	}

	var expiryMs int64
	if ownerID, _ := db.ClientOwner(clientID); ownerID != 0 {
		expiryMs = db.GetOperatorExpiry(ownerID)
	}

	email := randomEmail()
	if err := addClientToInbound(targetInboundID, email, details.Comment, details.Subscription, uuid, expiryMs); err != nil {
		return fmt.Errorf("ошибка добавления в inbound %d: %v", targetInboundID, err)
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
		first := true
		for ibID, email := range c.Emails {
			if !first {
				time.Sleep(200 * time.Millisecond)
			}
			if err := updateClientInInbound(ibID, email, c.Comment, c.Subscription, c.UUID, expiryMs); err != nil {
				log.Printf("⚠️ Ошибка обновления expiry клиента '%s' в inbound %d: %v", c.Comment, ibID, err)
			}
			first = false
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
