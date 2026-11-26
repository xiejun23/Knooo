package studio

import (
	"log"
	"net/http"
)

func desktopTopmost(topmost func(*bool) (bool, bool, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Pinned *bool `json:"pinned"`
		}
		if r.Method == http.MethodPost {
			if !decode(w, r, &body) {
				return
			}
			if body.Pinned == nil {
				http.Error(w, "缺少窗口置顶设置。", http.StatusBadRequest)
				return
			}
		}
		var available, pinned bool
		if topmost != nil {
			var err error
			available, pinned, err = topmost(body.Pinned)
			if err != nil {
				log.Printf("窗口置顶：%v", err)
				http.Error(w, "无法访问窗口置顶状态。", http.StatusInternalServerError)
				return
			}
		}
		if body.Pinned != nil && !available {
			http.Error(w, "窗口置顶仅在 Windows 桌面窗口中可用。", http.StatusConflict)
			return
		}
		jsonReply(w, struct {
			Available bool `json:"available"`
			Pinned    bool `json:"pinned"`
		}{Available: available, Pinned: pinned})
	}
}
