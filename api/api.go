package api

import (
	"encoding/json"
	"net/http"

	"github.com/layou233/zbproxy/v3/adapter"
	"github.com/layou233/zbproxy/v3/protocol/minecraft"
)

// statusResponse is the JSON structure returned by GET /api/status.
type statusResponse struct {
	Outbounds []outboundStatus `json:"outbounds"`
}

type outboundStatus struct {
	Name          string          `json:"name"`
	TargetAddress string          `json:"target_address"`
	TargetPort    uint16          `json:"target_port"`
	Minecraft     *mcStatus       `json:"minecraft,omitempty"`
}

type mcStatus struct {
	Online int32  `json:"online"`
	Max    int32  `json:"max"`
	OnlineLimitExceeded bool `json:"online_limit_exceeded"`
	MotdDescription string `json:"motd_description,omitempty"`
	MotdFavicon     string `json:"motd_favicon,omitempty"`
}

func Start(listenAddr string, outboundMap map[string]adapter.Outbound) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		resp := statusResponse{
			Outbounds: make([]outboundStatus, 0, len(outboundMap)),
		}
		for _, ob := range outboundMap {
			s := outboundStatus{
				Name: ob.Name(),
			}
			// Try to extract Minecraft-specific info
			if mcOb, ok := ob.(*minecraft.Outbound); ok {
				s.TargetAddress = mcOb.Config().TargetAddress
				s.TargetPort = mcOb.Config().TargetPort
				mcCfg := mcOb.Config().Minecraft
				if mcCfg != nil {
					max := mcCfg.OnlineCount.Max
					online := mcOb.OnlineCount()
					// Use configured Online if set (negative means use live count)
					if mcCfg.OnlineCount.Online >= 0 {
						online = mcCfg.OnlineCount.Online
					}
					s.Minecraft = &mcStatus{
						Online: online,
						Max:    max,
						OnlineLimitExceeded: mcCfg.OnlineCount.EnableMaxLimit && max <= online,
						MotdDescription:     mcCfg.MotdDescription,
						MotdFavicon:         mcCfg.MotdFavicon,
					}
				}
			}
			resp.Outbounds = append(resp.Outbounds, s)
		}

		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(resp)
	})

	return http.ListenAndServe(listenAddr, mux)
}
