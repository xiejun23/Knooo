package studio

import (
	"log"
	"net/http"
)

type desktopStartupState string

const (
	startupUnavailable desktopStartupState = "unavailable"
	startupOff         desktopStartupState = "off"
	startupOn          desktopStartupState = "on"
	startupBlocked     desktopStartupState = "blocked"
)

func (a *app) desktopStartup(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Enabled *bool `json:"enabled"`
	}
	if r.Method == http.MethodPost {
		if !decode(w, r, &request) {
			return
		}
		if request.Enabled == nil {
			http.Error(w, "缺少登录启动设置。", http.StatusBadRequest)
			return
		}
	}

	state := startupUnavailable
	if !a.demo {
		var err error
		state, err = windowsDesktopStartup(request.Enabled)
		if err != nil {
			log.Printf("Windows 登录启动设置：%v", err)
			http.Error(w, "无法访问 Windows 登录启动设置。", http.StatusInternalServerError)
			return
		}
	}
	if request.Enabled != nil && state == startupUnavailable {
		http.Error(w, "登录启动仅在 Windows 安装版中可用。", http.StatusConflict)
		return
	}
	jsonReply(w, struct {
		State desktopStartupState `json:"state"`
	}{State: state})
}
