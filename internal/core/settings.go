package core

import "strings"

// Mask is what the API shows instead of a stored secret. Saving the mask back keeps the old value.
const Mask = "********"

// SettingKeys lists the household settings and their defaults. Keys ending in a secret name are masked.
var SettingKeys = map[string]string{
	"household_name":     "Our Household",
	"currency":           "$",
	"bill_remind_hour":   "9",
	"allday_remind_hour": "8",
	"week_start":         "0", // 0 = Sunday, 1 = Monday
	"base_url":           "",
	"ntfy_server":        "https://ntfy.sh",
	"ntfy_token":         "",
	"smtp_host":          "",
	"smtp_port":          "587",
	"smtp_user":          "",
	"smtp_pass":          "",
	"smtp_from":          "",
	"twilio_sid":         "",
	"twilio_token":       "",
	"twilio_from":        "",
	"household_webhook":  "",
	"overdue_reminders":  "1",
}

func isSecret(k string) bool {
	return strings.HasSuffix(k, "_pass") || strings.HasSuffix(k, "_token") || k == "household_webhook"
}

// Settings returns every household setting. masked=true hides secrets (for the API).
func (s *Store) Settings(masked bool) map[string]string {
	out := map[string]string{}
	for k, v := range SettingKeys {
		out[k] = v
	}
	rows, err := s.DB.Query(`SELECT key, value FROM settings`)
	if err == nil {
		for rows.Next() {
			var k, v string
			rows.Scan(&k, &v)
			if _, ok := SettingKeys[k]; ok {
				out[k] = v
			}
		}
		rows.Close()
	}
	if masked {
		for k, v := range out {
			if isSecret(k) && v != "" {
				out[k] = Mask
			}
		}
	}
	return out
}

// Setting returns one setting.
func (s *Store) Setting(k string) string { return s.Settings(false)[k] }

// SaveSettings stores known keys. A masked secret is left unchanged.
func (s *Store) SaveSettings(in map[string]string) error {
	for k, v := range in {
		if _, ok := SettingKeys[k]; !ok {
			continue
		}
		if isSecret(k) && v == Mask {
			continue
		}
		if _, err := s.DB.Exec(`INSERT INTO settings(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, k, strings.TrimSpace(v)); err != nil {
			return err
		}
	}
	return nil
}
