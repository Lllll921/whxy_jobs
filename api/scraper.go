package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

func scrapeAll() []Job {
	var scraped []Job

	zcoolJobs := scrapeZCOOL()
	scraped = append(scraped, zcoolJobs...)

	validated := validateBuiltinJobs(builtinJobs())

	return mergeJobs(scraped, validated)
}

func scrapeZCOOL() []Job {
	client := &http.Client{Timeout: 15 * time.Second}

	// ZCOOL 的招聘列表页可能是 SPA，尝试获取并解析
	req, err := http.NewRequest("GET", "https://www.zcool.com.cn/opportunity/home", nil)
	if err != nil {
		return nil
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil
	}
	html := string(body)

	// 尝试从页面中提取 JSON 数据（很多 SSR 页面会嵌入初始数据）
	if jobs := extractZCOOLJSON(html); len(jobs) > 0 {
		return jobs
	}

	// 尝试从 HTML 中提取职位链接，然后逐个获取详情
	return extractZCOOLLinks(html, client)
}

func extractZCOOLJSON(html string) []Job {
	// 查找 __NEXT_DATA__ 或 window.__INITIAL_STATE__ 等常见模式
	patterns := []string{
		`__NEXT_DATA__[^>]*>(.*?)</script>`,
		`__INITIAL_STATE__\s*=\s*(\{.*?\});`,
		`"opportunityList"\s*:\s*(\[.*?\])`,
	}

	for _, pat := range patterns {
		re := regexp.MustCompile(pat)
		match := re.FindStringSubmatch(html)
		if len(match) > 1 {
			var raw []map[string]interface{}
			if err := json.Unmarshal([]byte(match[1]), &raw); err == nil {
				return convertZCOOLData(raw)
			}
		}
	}
	return nil
}

func convertZCOOLData(raw []map[string]interface{}) []Job {
	var jobs []Job
	for _, item := range raw {
		title, _ := item["title"].(string)
		if title == "" {
			continue
		}
		salary, _ := item["salary"].(string)
		if salary == "" {
			salary = "面议"
		}
		city, _ := item["city"].(string)
		if city == "" {
			city = "其他"
		}
		company, _ := item["company"].(string)
		id, _ := item["id"].(string)

		job := Job{
			Category: guessCategory(title),
			Title:    title,
			Salary:   salary,
			Company:  company + " | " + city,
			City:     normalizeCity(city),
			Recruit:  "社招",
			Apply:    "站酷平台在线投递",
			Deadline: "详见原文",
			URL:      "https://www.zcool.com.cn/opportunity/post/" + id + ".html",
		}
		jobs = append(jobs, job)
	}
	return jobs
}

func extractZCOOLLinks(html string, client *http.Client) []Job {
	linkRe := regexp.MustCompile(`/opportunity/post/([A-Za-z0-9+=]+)\.html`)
	matches := linkRe.FindAllStringSubmatch(html, -1)

	seen := make(map[string]bool)
	var ids []string
	for _, m := range matches {
		if len(m) < 2 || seen[m[1]] {
			continue
		}
		seen[m[1]] = true
		ids = append(ids, m[1])
		if len(ids) >= 15 {
			break
		}
	}

	var mu sync.Mutex
	var jobs []Job
	var wg sync.WaitGroup

	sem := make(chan struct{}, 5)
	for _, id := range ids {
		wg.Add(1)
		go func(jobID string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if job := fetchZCOOLDetail(jobID, client); job != nil {
				mu.Lock()
				jobs = append(jobs, *job)
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()

	return jobs
}

func fetchZCOOLDetail(id string, client *http.Client) *Job {
	url := "https://www.zcool.com.cn/opportunity/post/" + id + ".html"

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	if strings.Contains(html, "已过期") || strings.Contains(html, "已结束") || strings.Contains(html, "该职位已过期") {
		return nil
	}

	titleRe := regexp.MustCompile(`<title>([^<]*)</title>`)
	titleMatch := titleRe.FindStringSubmatch(html)
	title := ""
	if len(titleMatch) > 1 {
		title = strings.TrimSpace(titleMatch[1])
		title = strings.Split(title, " - ")[0]
		title = strings.Split(title, "- ")[0]
		title = strings.TrimSpace(title)
	}
	if title == "" || title == "站酷ZCOOL" {
		return nil
	}

	salaryRe := regexp.MustCompile(`(\d+)[Kk]\s*[-–]\s*(\d+)[Kk]`)
	salaryMatch := salaryRe.FindStringSubmatch(html)
	salary := "面议"
	if len(salaryMatch) > 0 {
		salary = salaryMatch[0]
	}

	city := extractCity(html)

	return &Job{
		Category: guessCategory(title),
		Title:    title,
		Salary:   salary,
		Company:  "站酷招聘 | " + city,
		City:     city,
		Recruit:  "社招",
		Apply:    "站酷平台在线投递",
		Deadline: "详见原文",
		URL:      url,
	}
}

// 批量验证内置岗位链接是否仍然有效
func validateBuiltinJobs(jobs []Job) []Job {
	var mu sync.Mutex
	var valid []Job
	var wg sync.WaitGroup

	client := &http.Client{Timeout: 8 * time.Second}
	sem := make(chan struct{}, 10)

	for _, job := range jobs {
		wg.Add(1)
		go func(j Job) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if isJobValid(j.URL, client) {
				mu.Lock()
				valid = append(valid, j)
				mu.Unlock()
			}
		}(job)
	}
	wg.Wait()

	// 如果验证后太少（网络问题），返回全部内置数据
	if len(valid) < len(jobs)/2 {
		return jobs
	}
	return valid
}

func isJobValid(url string, client *http.Client) bool {
	if url == "" {
		return false
	}

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return true // 无法验证则保留
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := client.Do(req)
	if err != nil {
		return true // 网络错误则保留
	}
	defer resp.Body.Close()

	if resp.StatusCode == 404 || resp.StatusCode == 410 {
		return false
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return true
	}
	html := string(body)

	if strings.Contains(html, "该职位已过期") || strings.Contains(html, "该岗位已关闭") {
		return false
	}

	return true
}

func mergeJobs(scraped, builtin []Job) []Job {
	urlSet := make(map[string]bool)
	var result []Job

	for _, j := range scraped {
		if j.URL != "" {
			urlSet[j.URL] = true
		}
		result = append(result, j)
	}

	for _, j := range builtin {
		if j.URL != "" && urlSet[j.URL] {
			continue
		}
		result = append(result, j)
	}

	return result
}

func guessCategory(title string) string {
	t := strings.ToLower(title)
	switch {
	case containsAny(t, "剪辑", "后期", "视频制作", "ae", "pr"):
		return "edit"
	case containsAny(t, "运营", "小红书", "抖音", "新媒体运营"):
		return "operate"
	case containsAny(t, "摄影", "摄像", "拍摄", "编导"):
		return "photo"
	case containsAny(t, "记者", "编辑", "采编", "新闻"):
		return "news"
	case containsAny(t, "空间", "展览", "室内", "展厅", "展陈", "环境"):
		return "space"
	default:
		return "design"
	}
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func normalizeCity(raw string) string {
	cities := []string{"北京", "上海", "广州", "深圳", "杭州", "成都", "武汉", "长沙", "南京", "天津", "重庆", "苏州", "西安"}
	for _, c := range cities {
		if strings.Contains(raw, c) {
			return c
		}
	}
	return "其他"
}

func extractCity(html string) string {
	cityRe := regexp.MustCompile(`(北京|上海|广州|深圳|杭州|成都|武汉|长沙|南京|天津|重庆|苏州|西安)`)
	m := cityRe.FindStringSubmatch(html)
	if len(m) > 1 {
		return m[1]
	}
	return "其他"
}

func nowBeijing() string {
	loc, _ := time.LoadLocation("Asia/Shanghai")
	return time.Now().In(loc).Format("2006-01-02 15:04:05")
}

func fmtLog(format string, args ...interface{}) string {
	return fmt.Sprintf("[%s] %s", nowBeijing(), fmt.Sprintf(format, args...))
}
