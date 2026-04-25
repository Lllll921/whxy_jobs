package handler

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"
	"time"
)

type JobsResponse struct {
	Jobs      []Job  `json:"jobs"`
	UpdatedAt string `json:"updatedAt"`
}

var (
	memCache   *JobsResponse
	memCacheMu sync.RWMutex
)

func loadJobs() *JobsResponse {
	if data := loadFromBlob(); data != nil {
		return data
	}

	memCacheMu.RLock()
	defer memCacheMu.RUnlock()
	if memCache != nil {
		return memCache
	}

	return &JobsResponse{
		Jobs:      builtinJobs(),
		UpdatedAt: "内置数据（未配置Blob Storage）",
	}
}

func saveJobs(data *JobsResponse) {
	memCacheMu.Lock()
	memCache = data
	memCacheMu.Unlock()

	saveToBlob(data)
}

// ---------- Vercel Blob Storage ----------
// 就是存/读一个 JSON 文件，不需要数据库

const blobFileName = "jobs-data.json"

func loadFromBlob() *JobsResponse {
	token := os.Getenv("BLOB_READ_WRITE_TOKEN")
	if token == "" {
		return nil
	}

	// 第一步：通过 list API 找到文件的下载地址
	req, err := http.NewRequest("GET", "https://blob.vercel-storage.com?prefix="+blobFileName, nil)
	if err != nil {
		return nil
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-api-version", "7")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var listResult struct {
		Blobs []struct {
			URL         string `json:"url"`
			DownloadURL string `json:"downloadUrl"`
		} `json:"blobs"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&listResult); err != nil || len(listResult.Blobs) == 0 {
		return nil
	}

	// 第二步：下载 JSON 文件
	downloadURL := listResult.Blobs[0].DownloadURL
	if downloadURL == "" {
		downloadURL = listResult.Blobs[0].URL
	}

	resp2, err := client.Get(downloadURL)
	if err != nil {
		return nil
	}
	defer resp2.Body.Close()

	body, err := io.ReadAll(resp2.Body)
	if err != nil {
		return nil
	}

	var data JobsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return nil
	}
	return &data
}

func saveToBlob(data *JobsResponse) {
	token := os.Getenv("BLOB_READ_WRITE_TOKEN")
	if token == "" {
		return
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return
	}

	// 上传 JSON 文件（覆盖已有文件）
	req, err := http.NewRequest("PUT", "https://blob.vercel-storage.com/"+blobFileName, bytes.NewReader(jsonData))
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-api-version", "7")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-add-random-suffix", "0")

	client := &http.Client{Timeout: 8 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return
	}
	resp.Body.Close()
}
