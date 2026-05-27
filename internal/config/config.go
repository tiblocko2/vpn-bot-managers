package config

import (
	"encoding/json"
	"os"
)

type InboundConfig struct {
	ID    int64  `json:"id"`
	Label string `json:"label"`
}

type Config struct {
	BotToken      string          `json:"bot_token"`
	SuperUserID   int64           `json:"super_user_id"`
	PanelURL      string          `json:"panel_url"`
	PanelUsername string          `json:"panel_username"`
	PanelPassword string          `json:"panel_password"`
	PanelAPIToken string          `json:"panel_api_token,omitempty"`
	SubDomain     string          `json:"sub_domain"`
	ProxyURL      string          `json:"proxy_url"`
	Inbounds      []InboundConfig `json:"inbounds"`
	DBPath        string          `json:"db_path"`

	// Legacy fields — kept only for one-time migration from v1.0/v1.1 configs.
	// After migration these are cleared and removed from config.json.
	VlessInboundID int64 `json:"vless_inbound_id,omitempty"`
	VmessInboundID int64 `json:"vmess_inbound_id,omitempty"`
}

var Cfg *Config
var configPath string

func Load(path string) error {
	configPath = path
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	c := &Config{}
	if err := json.Unmarshal(data, c); err != nil {
		return err
	}
	// Auto-migrate from old two-inbound format. Legacy IDs are kept in the
	// struct so main.go can use them for the one-time DB migration.
	if len(c.Inbounds) == 0 {
		if c.VlessInboundID != 0 {
			c.Inbounds = append(c.Inbounds, InboundConfig{ID: c.VlessInboundID, Label: "VLESS"})
		}
		if c.VmessInboundID != 0 {
			c.Inbounds = append(c.Inbounds, InboundConfig{ID: c.VmessInboundID, Label: "VMess"})
		}
	}
	Cfg = c
	return nil
}

func Save() error {
	data, err := json.MarshalIndent(Cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(configPath, data, 0600)
}

func SetSubDomain(domain string) error {
	Cfg.SubDomain = domain
	return Save()
}

func AddInbound(ib InboundConfig) error {
	for _, existing := range Cfg.Inbounds {
		if existing.ID == ib.ID {
			return nil // already present
		}
	}
	Cfg.Inbounds = append(Cfg.Inbounds, ib)
	return Save()
}

func RemoveInbound(id int64) error {
	updated := Cfg.Inbounds[:0]
	for _, ib := range Cfg.Inbounds {
		if ib.ID != id {
			updated = append(updated, ib)
		}
	}
	Cfg.Inbounds = updated
	return Save()
}
