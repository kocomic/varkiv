package agenttray

import (
	"fmt"
	"strings"

	"varkiv/internal/deviceagent"
)

type menuText struct {
	Title      string
	SyncNow    string
	OpenStatus string
	Exit       string
	Never      string
	Running    string
	Complete   string
	Conflict   string
	Failed     string
	Counts     string
}

func normalizeLocale(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch {
	case strings.HasPrefix(value, "zh-tw"), strings.HasPrefix(value, "zh-hk"), strings.HasPrefix(value, "zh-mo"), strings.HasPrefix(value, "zh-hant"):
		return "zh-TW"
	case strings.HasPrefix(value, "zh"):
		return "zh-CN"
	case strings.HasPrefix(value, "ja"):
		return "ja"
	default:
		return "en"
	}
}

func textForLocale(locale string) menuText {
	switch normalizeLocale(locale) {
	case "zh-CN":
		return menuText{Title: "Varkiv", SyncNow: "立即同步", OpenStatus: "打开同步状态", Exit: "退出托盘", Never: "尚未同步", Running: "正在同步", Complete: "同步完成", Conflict: "有冲突待处理", Failed: "同步失败", Counts: "上传 %d · 下载 %d"}
	case "zh-TW":
		return menuText{Title: "Varkiv", SyncNow: "立即同步", OpenStatus: "開啟同步狀態", Exit: "結束常駐程式", Never: "尚未同步", Running: "正在同步", Complete: "同步完成", Conflict: "有衝突待處理", Failed: "同步失敗", Counts: "上傳 %d · 下載 %d"}
	case "ja":
		return menuText{Title: "Varkiv", SyncNow: "今すぐ同期", OpenStatus: "同期状況を開く", Exit: "常駐を終了", Never: "未同期", Running: "同期中", Complete: "同期完了", Conflict: "競合を確認してください", Failed: "同期失敗", Counts: "アップロード %d · ダウンロード %d"}
	default:
		return menuText{Title: "Varkiv", SyncNow: "Sync now", OpenStatus: "Open sync status", Exit: "Exit tray", Never: "Not synced yet", Running: "Syncing", Complete: "Sync complete", Conflict: "Conflict needs review", Failed: "Sync failed", Counts: "%d uploaded · %d downloaded"}
	}
}

func statusText(text menuText, status *deviceagent.AgentSyncStatus) string {
	if status == nil {
		return text.Never
	}
	switch status.State {
	case "running":
		return text.Running
	case "complete":
		if status.Uploaded == 0 && status.Downloaded == 0 {
			return text.Complete
		}
		return fmt.Sprintf("%s · %s", text.Complete, fmt.Sprintf(text.Counts, status.Uploaded, status.Downloaded))
	case "conflict":
		return text.Conflict
	case "failed":
		return text.Failed
	default:
		return text.Never
	}
}
