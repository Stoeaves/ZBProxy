package minecraft

import (
	"encoding/json"
	"sync/atomic"

	"github.com/layou233/zbproxy/v3/config"
	"github.com/layou233/zbproxy/v3/version"
)

type motdObject struct {
	Version struct {
		Name     string `json:"name"`
		Protocol uint   `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int32 `json:"max"`
		Online int32 `json:"online"`
		Sample any   `json:"sample,omitempty"`
	} `json:"players"`
	Description struct {
		Text string `json:"text,omitempty"`
	} `json:"description,omitempty"`
	Favicon string `json:"favicon,omitempty"`
}

type playerSample struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

func generateMOTD(protocolVersion uint, s *config.Outbound, onlineCount *atomic.Int32) []byte {
	online := s.Minecraft.OnlineCount.Online
	if online < 0 {
		online = onlineCount.Load()
	}

	motd, _ := json.Marshal(motdObject{
		Version: struct {
			Name     string `json:"name"`
			Protocol uint   `json:"protocol"`
		}{
			Name:     "ZBProxy " + version.Version,
			Protocol: protocolVersion,
		},
		Players: struct {
			Max    int32 `json:"max"`
			Online int32 `json:"online"`
			Sample any   `json:"sample,omitempty"`
		}{
			Max:    s.Minecraft.OnlineCount.Max,
			Online: online,
			Sample: s.Minecraft.OnlineCount.Sample,
		},
		Description: struct {
			Text string `json:"text,omitempty"`
		}{
			Text: s.Minecraft.MotdDescription,
		},
		Favicon: s.Minecraft.MotdFavicon,
	})

	return motd
}

const defaultMOTD = `data:image/png;base64,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`
