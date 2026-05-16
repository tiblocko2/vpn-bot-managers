package db

import (
	"database/sql"
	"fmt"
	"log"

	"vpn-bot/internal/config"

	_ "modernc.org/sqlite"
)

var conn *sql.DB

const ClientsPerPage = 10

type ClientRecord struct {
	ID      int64
	Comment string
}

type OperatorRecord struct {
	UserID     int64
	ExpiresAt  int64 // Unix milliseconds; 0 = no expiry
	MaxClients int
	Label      string
}

type ClientDetails struct {
	ID           int64
	Comment      string
	Subscription string
	UUID         string
	Emails       map[int64]string
}

func Init() {
	var err error
	conn, err = sql.Open("sqlite", config.Cfg.DBPath)
	if err != nil {
		panic(err)
	}

	conn.Exec("PRAGMA journal_mode=WAL")
	conn.Exec("PRAGMA busy_timeout=5000")
	conn.Exec("PRAGMA foreign_keys=ON")
	conn.SetMaxOpenConns(1)

	_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS operators (
		user_id INTEGER PRIMARY KEY,
		expires_at INTEGER NOT NULL DEFAULT 0,
		max_clients INTEGER NOT NULL DEFAULT 6,
		label TEXT NOT NULL DEFAULT ''
	)`)
	if err != nil {
		panic(err)
	}
	conn.Exec(`ALTER TABLE operators ADD COLUMN expires_at INTEGER NOT NULL DEFAULT 0`)
	conn.Exec(`ALTER TABLE operators ADD COLUMN max_clients INTEGER NOT NULL DEFAULT 6`)
	conn.Exec(`ALTER TABLE operators ADD COLUMN label TEXT NOT NULL DEFAULT ''`)

	// Legacy columns email_vless/email_vmess kept for backward compat (always '').
	_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS clients (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		comment TEXT NOT NULL,
		subscription TEXT NOT NULL DEFAULT '',
		uuid TEXT NOT NULL DEFAULT '',
		email_vless TEXT NOT NULL DEFAULT '',
		email_vmess TEXT NOT NULL DEFAULT '',
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		panic(err)
	}

	conn.Exec(`CREATE INDEX IF NOT EXISTS idx_clients_comment ON clients(comment)`)

	// client_emails is the authoritative source for inbound→email mapping.
	_, err = conn.Exec(`CREATE TABLE IF NOT EXISTS client_emails (
		client_id INTEGER NOT NULL,
		inbound_id INTEGER NOT NULL,
		email TEXT NOT NULL,
		PRIMARY KEY (client_id, inbound_id),
		FOREIGN KEY (client_id) REFERENCES clients(id) ON DELETE CASCADE
	)`)
	if err != nil {
		panic(err)
	}

	// One-time column migrations for existing databases.
	if _, err = conn.Exec(`ALTER TABLE clients ADD COLUMN subscription TEXT NOT NULL DEFAULT ''`); err != nil {
		log.Printf("ℹ️ Колонка subscription уже существует")
	}
	conn.Exec(`ALTER TABLE clients ADD COLUMN uuid TEXT NOT NULL DEFAULT ''`)
	conn.Exec(`ALTER TABLE clients ADD COLUMN owner_id INTEGER NOT NULL DEFAULT 0`)
}

// MigrateEmailsFromOldSchema copies email_vless/email_vmess data into
// client_emails using the two legacy inbound IDs. Runs once after upgrading from v1.0/v1.1.
func MigrateEmailsFromOldSchema(vlessID, vmessID int64) {
	if vlessID == 0 && vmessID == 0 {
		return
	}
	var existing int
	conn.QueryRow("SELECT COUNT(*) FROM client_emails").Scan(&existing)
	if existing > 0 {
		return
	}
	var count int
	conn.QueryRow("SELECT COUNT(*) FROM clients WHERE email_vless != '' OR email_vmess != ''").Scan(&count)
	if count == 0 {
		return
	}

	rows, err := conn.Query("SELECT id, email_vless, email_vmess FROM clients WHERE email_vless != '' OR email_vmess != ''")
	if err != nil {
		log.Printf("⚠️ Ошибка миграции emails: %v", err)
		return
	}
	type row struct{ id int64; ev, em string }
	var records []row
	for rows.Next() {
		var r row
		rows.Scan(&r.id, &r.ev, &r.em)
		records = append(records, r)
	}
	rows.Close()

	for _, r := range records {
		if vlessID != 0 && r.ev != "" {
			conn.Exec("INSERT OR IGNORE INTO client_emails VALUES (?, ?, ?)", r.id, vlessID, r.ev)
		}
		if vmessID != 0 && r.em != "" {
			conn.Exec("INSERT OR IGNORE INTO client_emails VALUES (?, ?, ?)", r.id, vmessID, r.em)
		}
	}
	log.Printf("✅ Мигрировано %d клиентов в client_emails", len(records))
}

func IsOperator(userID int64) bool {
	if userID == config.Cfg.SuperUserID {
		return true
	}
	var id int64
	return conn.QueryRow("SELECT user_id FROM operators WHERE user_id = ?", userID).Scan(&id) == nil
}

func AddOperator(userID int64, label string) error {
	_, err := conn.Exec("INSERT OR IGNORE INTO operators (user_id, label) VALUES (?, ?)", userID, label)
	return err
}

func SetOperatorLabel(userID int64, label string) error {
	_, err := conn.Exec("UPDATE operators SET label = ? WHERE user_id = ?", label, userID)
	return err
}

// GetOperatorLabel returns the stored label for a manager.
func GetOperatorLabel(userID int64) string {
	var label string
	conn.QueryRow("SELECT label FROM operators WHERE user_id = ?", userID).Scan(&label)
	return label
}

func RemoveOperator(userID int64) error {
	_, err := conn.Exec("DELETE FROM operators WHERE user_id = ?", userID)
	return err
}

func GetAllOperators() ([]int64, error) {
	rows, err := conn.Query("SELECT user_id FROM operators")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ops = append(ops, id)
	}
	return ops, nil
}

func GetAllOperatorsWithExpiry() ([]OperatorRecord, error) {
	rows, err := conn.Query("SELECT user_id, expires_at, max_clients, label FROM operators")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ops []OperatorRecord
	for rows.Next() {
		var r OperatorRecord
		if err := rows.Scan(&r.UserID, &r.ExpiresAt, &r.MaxClients, &r.Label); err != nil {
			return nil, err
		}
		ops = append(ops, r)
	}
	return ops, nil
}

func GetOperatorMaxClients(userID int64) int {
	var maxClients int
	if err := conn.QueryRow("SELECT max_clients FROM operators WHERE user_id = ?", userID).Scan(&maxClients); err != nil {
		return 6
	}
	return maxClients
}

func SetOperatorMaxClients(userID int64, maxClients int) error {
	_, err := conn.Exec("UPDATE operators SET max_clients = ? WHERE user_id = ?", maxClients, userID)
	return err
}

func GetOperatorExpiry(userID int64) int64 {
	var expiresAt int64
	conn.QueryRow("SELECT expires_at FROM operators WHERE user_id = ?", userID).Scan(&expiresAt)
	return expiresAt
}

func SetOperatorExpiry(userID int64, expiryMs int64) error {
	_, err := conn.Exec("UPDATE operators SET expires_at = ? WHERE user_id = ?", expiryMs, userID)
	return err
}

// GetClientsByOwner returns all clients for a given manager with their inbound emails.
func GetClientsByOwner(ownerID int64) ([]ClientDetails, error) {
	rows, err := conn.Query("SELECT id, comment, subscription, uuid FROM clients WHERE owner_id = ?", ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var clients []ClientDetails
	for rows.Next() {
		var d ClientDetails
		rows.Scan(&d.ID, &d.Comment, &d.Subscription, &d.UUID)
		clients = append(clients, d)
	}
	for i := range clients {
		eRows, err := conn.Query("SELECT inbound_id, email FROM client_emails WHERE client_id = ?", clients[i].ID)
		if err != nil {
			continue
		}
		clients[i].Emails = make(map[int64]string)
		for eRows.Next() {
			var ibID int64
			var email string
			eRows.Scan(&ibID, &email)
			clients[i].Emails[ibID] = email
		}
		eRows.Close()
	}
	return clients, nil
}

func ClientExists(comment string) bool {
	var count int
	err := conn.QueryRow("SELECT COUNT(*) FROM clients WHERE comment = ?", comment).Scan(&count)
	return err == nil && count > 0
}

func ClientExistsBySubscription(subscription string) bool {
	var count int
	err := conn.QueryRow("SELECT COUNT(*) FROM clients WHERE subscription = ?", subscription).Scan(&count)
	return err == nil && count > 0
}

// GetClientCountByOwner returns how many clients a manager has created.
func GetClientCountByOwner(ownerID int64) int {
	var count int
	conn.QueryRow("SELECT COUNT(*) FROM clients WHERE owner_id = ?", ownerID).Scan(&count)
	return count
}

// ClientOwner returns the owner_id for a client record.
func ClientOwner(clientID int64) (int64, error) {
	var ownerID int64
	err := conn.QueryRow("SELECT owner_id FROM clients WHERE id = ?", clientID).Scan(&ownerID)
	return ownerID, err
}

// SaveClient creates a client record and stores one email per inbound.
// ownerID=0 means the client was imported or created by the superuser with no manager restriction.
func SaveClient(comment, subscription, uuid string, emails map[int64]string, ownerID int64) error {
	result, err := conn.Exec(
		"INSERT INTO clients (comment, subscription, uuid, email_vless, email_vmess, owner_id) VALUES (?, ?, ?, '', '', ?)",
		comment, subscription, uuid, ownerID,
	)
	if err != nil {
		return err
	}
	clientID, _ := result.LastInsertId()
	for inboundID, email := range emails {
		if _, err := conn.Exec(
			"INSERT OR IGNORE INTO client_emails (client_id, inbound_id, email) VALUES (?, ?, ?)",
			clientID, inboundID, email,
		); err != nil {
			return fmt.Errorf("ошибка записи email для inbound %d: %v", inboundID, err)
		}
	}
	return nil
}

// GetClientEmails returns all inbound→email pairs for a client and its display name.
// Used by panel.DeleteClient.
func GetClientEmails(clientID int64) (emails map[int64]string, name string, err error) {
	err = conn.QueryRow("SELECT comment FROM clients WHERE id = ?", clientID).Scan(&name)
	if err != nil {
		return nil, "", fmt.Errorf("клиент не найден")
	}
	rows, err := conn.Query("SELECT inbound_id, email FROM client_emails WHERE client_id = ?", clientID)
	if err != nil {
		return nil, name, err
	}
	defer rows.Close()
	emails = make(map[int64]string)
	for rows.Next() {
		var ibID int64
		var email string
		if err = rows.Scan(&ibID, &email); err != nil {
			return
		}
		emails[ibID] = email
	}
	return
}

// GetClientDetails returns full client info including subscription, uuid, and inbound emails.
func GetClientDetails(id int64) (ClientDetails, error) {
	var d ClientDetails
	d.ID = id
	err := conn.QueryRow(
		"SELECT comment, subscription, uuid FROM clients WHERE id = ?", id,
	).Scan(&d.Comment, &d.Subscription, &d.UUID)
	if err != nil {
		return d, fmt.Errorf("клиент не найден")
	}
	rows, err := conn.Query("SELECT inbound_id, email FROM client_emails WHERE client_id = ?", id)
	if err != nil {
		return d, err
	}
	defer rows.Close()
	d.Emails = make(map[int64]string)
	for rows.Next() {
		var ibID int64
		var email string
		rows.Scan(&ibID, &email)
		d.Emails[ibID] = email
	}
	return d, nil
}

// AddClientEmail inserts a new inbound→email mapping for an existing client.
func AddClientEmail(clientID, inboundID int64, email string) error {
	_, err := conn.Exec(
		"INSERT OR IGNORE INTO client_emails (client_id, inbound_id, email) VALUES (?, ?, ?)",
		clientID, inboundID, email,
	)
	return err
}

// SetClientUUID persists the UUID for a client that was missing one.
func SetClientUUID(clientID int64, uuid string) {
	conn.Exec("UPDATE clients SET uuid = ? WHERE id = ?", uuid, clientID)
}

// GetClientsPage returns one page of clients and total count.
// ownerID=0 means superuser — returns all clients. Otherwise filters by owner.
func GetClientsPage(page int, ownerID int64) (clients []ClientRecord, total int, err error) {
	if ownerID == 0 {
		err = conn.QueryRow("SELECT COUNT(*) FROM clients").Scan(&total)
		if err != nil {
			return
		}
		rows, qerr := conn.Query(
			"SELECT id, comment FROM clients ORDER BY created_at DESC LIMIT ? OFFSET ?",
			ClientsPerPage, page*ClientsPerPage,
		)
		if qerr != nil {
			err = qerr
			return
		}
		defer rows.Close()
		for rows.Next() {
			var c ClientRecord
			if err = rows.Scan(&c.ID, &c.Comment); err != nil {
				return
			}
			clients = append(clients, c)
		}
		return
	}
	err = conn.QueryRow("SELECT COUNT(*) FROM clients WHERE owner_id = ?", ownerID).Scan(&total)
	if err != nil {
		return
	}
	rows, err := conn.Query(
		"SELECT id, comment FROM clients WHERE owner_id = ? ORDER BY created_at DESC LIMIT ? OFFSET ?",
		ownerID, ClientsPerPage, page*ClientsPerPage,
	)
	if err != nil {
		return
	}
	defer rows.Close()
	for rows.Next() {
		var c ClientRecord
		if err = rows.Scan(&c.ID, &c.Comment); err != nil {
			return
		}
		clients = append(clients, c)
	}
	return
}

func DeleteClient(id int64) error {
	_, err := conn.Exec("DELETE FROM clients WHERE id = ?", id)
	return err
}
