package studio

import (
	"encoding/json"

	"github.com/xiejun23/Knooo/internal/distill"
)

func modelKeyBinding(profile modelConnection, config distill.Config) []byte {
	binding, _ := json.Marshal([4]string{profile.ID, profile.Preset, config.Provider, config.BaseURL})
	return binding
}

func (a *app) savedModelKey(profile modelConnection, config distill.Config) string {
	if a.data == "" {
		return a.keys.values[profile.ID]
	}
	var secret []byte
	if a.db.QueryRow(`SELECT secret FROM model_keys WHERE id=?`, profile.ID).Scan(&secret) != nil {
		return ""
	}
	return a.unlockModelKey(profile, config, secret)
}

func (a *app) unlockModelKey(profile modelConnection, config distill.Config, secret []byte) string {
	if len(secret) == 0 {
		return ""
	}
	key, err := protectModelKey(a.data, secret, modelKeyBinding(profile, config), true)
	if err != nil {
		return ""
	}
	return string(key)
}

func (a *app) writeModelConnection(profile modelConnection, secret []byte, remove bool) error {
	tx, err := a.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if remove {
		_, err = tx.Exec(`DELETE FROM settings WHERE name=?`, "model:"+profile.ID)
	} else {
		content, _ := json.Marshal(profile)
		_, err = tx.Exec(`INSERT INTO settings VALUES(?,?) ON CONFLICT(name) DO UPDATE SET content=excluded.content`, "model:"+profile.ID, string(content))
	}
	if err != nil {
		return err
	}
	if len(secret) == 0 {
		_, err = tx.Exec(`DELETE FROM model_keys WHERE id=?`, profile.ID)
	} else {
		_, err = tx.Exec(`INSERT INTO model_keys VALUES(?,?) ON CONFLICT(id) DO UPDATE SET secret=excluded.secret`, profile.ID, secret)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}
