package handler

import (
	"encoding/json"
	"net/http"
)

func Handler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// Vercel Cron 触发刷新: GET /api/jobs?refresh=1
	if r.URL.Query().Get("refresh") == "1" {
		jobs := scrapeAll()
		data := &JobsResponse{
			Jobs:      jobs,
			UpdatedAt: nowBeijing(),
		}
		saveJobs(data)

		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"jobCount":  len(jobs),
			"updatedAt": data.UpdatedAt,
		})
		return
	}

	// 正常请求：返回岗位数据
	data := loadJobs()
	json.NewEncoder(w).Encode(data)
}
