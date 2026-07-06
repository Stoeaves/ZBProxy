package minecraft

import (
	"fmt"
	"time"

	"github.com/layou233/zbproxy/v3/common/mcprotocol"
	"github.com/layou233/zbproxy/v3/config"
)

func generateKickMessage(s *config.Outbound, name string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "连接失败\n"},

			{Text: "您的连接已被拒绝\n"},
			{Text: "原因: "},
			{Color: mcprotocol.LightPurple, Text: "您没有权限访问此服务器\n"},
			{Text: "请检查服务是否过期或者是否购买\n"},
			{Color: mcprotocol.Gold, Text: "若已购买请稍等片刻再连，数据同步需要时间\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s\n",
					time.Now().UnixMilli(), name),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}

func generatePlayerNumberLimitExceededMessage(s *config.Outbound, name string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "连接失败\n"},

			{Text: "您的连接已被拒绝\n"},
			{Text: "原因: "},
			{Color: mcprotocol.LightPurple, Text: "服务器最大人数已达到上限\n"},
			{Text: "联系管理员寻求帮助\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s\n",
					time.Now().UnixMilli(), name),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}

func generatePlayerNameUpdated(s *config.Outbound, name string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "Stiper"},
			{Text: "\n"},

			{Text: "已检测到您的游戏ID已修改\n"},
			{Color: mcprotocol.LightPurple, Text: "已自动为您修改绑定套餐的游戏ID\n"},
			{Text: "请重新进入服务器\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s\n",
					time.Now().UnixMilli(), name),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}

func generatePlayerNotBoundQQ(s *config.Outbound, name string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "Stiper"},
			{Text: "\n"},

			{Text: "您还未绑定QQ号\n"},
			{Color: mcprotocol.LightPurple, Text: "请前往官网绑定QQ\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s\n",
					time.Now().UnixMilli(), name),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}

func generateBanKickMessage(s *config.Outbound, name string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "连接失败\n"},

			{Text: "您的连接已被拒绝\n"},
			{Text: "原因: "},
			{Color: mcprotocol.LightPurple, Text: "您已被服务器封禁\n"},
			{Text: "请前往官网查看详情\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s\n",
					time.Now().UnixMilli(), name),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}

func generateUnknownErrorMessage(s *config.Outbound, name string, errorType string) mcprotocol.Message {
	return mcprotocol.Message{
		Color: mcprotocol.White,
		Extra: []mcprotocol.Message{
			{Bold: true, Color: mcprotocol.Red, Text: "温馨"},
			{Bold: true, Text: "提示"},
			{Text: " - "},
			{Bold: true, Color: mcprotocol.Gold, Text: "连接失败\n"},

			{Text: "您的连接已被拒绝\n"},
			{Text: "原因: "},
			{Color: mcprotocol.LightPurple, Text: "未知错误\n"},
			{Text: "请将截图发送给管理员\n\n"},

			{
				Color: mcprotocol.Gray,
				Text: fmt.Sprintf("时间戳: %d | 玩家名: %s | 错误类型: %s\n",
					time.Now().UnixMilli(), name, errorType),
			},
			{Text: "爱发电: "},
			{
				Color: mcprotocol.Aqua, UnderLined: true,
				Text: "https://ifdian.net/a/stoeaves\n",
			},
			{Text: "官网: "},
			{
				Color: mcprotocol.Gray, UnderLined: true,
				Text: "https://stiper.im",
			},
		},
	}
}