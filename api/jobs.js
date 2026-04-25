const fs = require('fs');
const path = require('path');

const BLOB_FILE = 'jobs-data.json';

function builtinJobs() {
  const raw = fs.readFileSync(path.join(__dirname, 'data.json'), 'utf-8');
  return JSON.parse(raw);
}

function nowBeijing() {
  return new Date().toLocaleString('zh-CN', {
    timeZone: 'Asia/Shanghai',
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    hour12: false
  });
}

// ---------- Vercel Blob Storage: 读写 JSON 文件 ----------

async function loadFromBlob() {
  const token = process.env.BLOB_READ_WRITE_TOKEN;
  if (!token) return null;

  try {
    const listRes = await fetch(`https://blob.vercel-storage.com?prefix=${BLOB_FILE}`, {
      headers: { 'Authorization': `Bearer ${token}`, 'x-api-version': '7' }
    });
    const { blobs } = await listRes.json();
    if (!blobs || blobs.length === 0) return null;

    const dataRes = await fetch(blobs[0].downloadUrl || blobs[0].url);
    return await dataRes.json();
  } catch {
    return null;
  }
}

async function saveToBlob(data) {
  const token = process.env.BLOB_READ_WRITE_TOKEN;
  if (!token) return;

  try {
    await fetch(`https://blob.vercel-storage.com/${BLOB_FILE}`, {
      method: 'PUT',
      headers: {
        'Authorization': `Bearer ${token}`,
        'x-api-version': '7',
        'Content-Type': 'application/json',
        'x-add-random-suffix': '0'
      },
      body: JSON.stringify(data)
    });
  } catch {}
}

// ---------- 爬虫：ZCOOL + 链接验证 ----------

async function fetchWithTimeout(url, opts = {}, timeout = 10000) {
  const controller = new AbortController();
  const id = setTimeout(() => controller.abort(), timeout);
  try {
    const res = await fetch(url, { ...opts, signal: controller.signal });
    clearTimeout(id);
    return res;
  } catch {
    clearTimeout(id);
    return null;
  }
}

function guessCategory(title) {
  const t = title.toLowerCase();
  if (/剪辑|后期|视频制作|ae|pr/.test(t)) return 'edit';
  if (/运营|小红书|抖音|新媒体运营/.test(t)) return 'operate';
  if (/摄影|摄像|拍摄|编导/.test(t)) return 'photo';
  if (/记者|编辑|采编|新闻/.test(t)) return 'news';
  if (/空间|展览|室内|展厅|展陈|环境/.test(t)) return 'space';
  return 'design';
}

function extractCity(text) {
  const cities = ['北京','上海','广州','深圳','杭州','成都','武汉','长沙','南京','天津','重庆','苏州','西安'];
  for (const c of cities) {
    if (text.includes(c)) return c;
  }
  return '其他';
}

async function scrapeZCOOL() {
  const jobs = [];
  try {
    const res = await fetchWithTimeout('https://www.zcool.com.cn/opportunity/home', {
      headers: {
        'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36',
        'Accept-Language': 'zh-CN,zh;q=0.9'
      }
    }, 15000);
    if (!res) return jobs;

    const html = await res.text();
    const linkRe = /\/opportunity\/post\/([A-Za-z0-9+=]+)\.html/g;
    const seen = new Set();
    const ids = [];
    let match;
    while ((match = linkRe.exec(html)) && ids.length < 15) {
      if (!seen.has(match[1])) {
        seen.add(match[1]);
        ids.push(match[1]);
      }
    }

    const results = await Promise.allSettled(
      ids.slice(0, 10).map(id => fetchZCOOLDetail(id))
    );
    for (const r of results) {
      if (r.status === 'fulfilled' && r.value) jobs.push(r.value);
    }
  } catch {}
  return jobs;
}

async function fetchZCOOLDetail(id) {
  const url = `https://www.zcool.com.cn/opportunity/post/${id}.html`;
  const res = await fetchWithTimeout(url, {
    headers: { 'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36' }
  });
  if (!res) return null;

  const html = await res.text();
  if (/已过期|已结束|该职位已过期/.test(html)) return null;

  const titleMatch = html.match(/<title>([^<]*)<\/title>/);
  let title = titleMatch ? titleMatch[1].split(' - ')[0].split('- ')[0].trim() : '';
  if (!title || title === '站酷ZCOOL') return null;

  const salaryMatch = html.match(/(\d+[Kk]\s*[-–]\s*\d+[Kk])/);
  const salary = salaryMatch ? salaryMatch[1] : '面议';
  const city = extractCity(html);

  return {
    category: guessCategory(title),
    tags: '', city, title, salary,
    company: '站酷招聘 | ' + city,
    recruit: '社招',
    requirements: [],
    apply: '站酷平台在线投递',
    deadline: '详见原文',
    url, extraTag: ''
  };
}

async function validateBuiltinJobs(jobs) {
  const results = await Promise.allSettled(
    jobs.map(async (job) => {
      if (!job.url) return null;
      try {
        const res = await fetchWithTimeout(job.url, {
          headers: { 'User-Agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36' }
        }, 8000);
        if (!res) return job;
        if (res.status === 404 || res.status === 410) return null;
        const html = await res.text();
        if (/该职位已过期|该岗位已关闭/.test(html)) return null;
        return job;
      } catch {
        return job;
      }
    })
  );

  const valid = results
    .filter(r => r.status === 'fulfilled' && r.value)
    .map(r => r.value);

  return valid.length >= jobs.length / 2 ? valid : jobs;
}

function mergeJobs(scraped, builtin) {
  const urlSet = new Set(scraped.filter(j => j.url).map(j => j.url));
  return [...scraped, ...builtin.filter(j => !j.url || !urlSet.has(j.url))];
}

async function scrapeAll() {
  const zcoolJobs = await scrapeZCOOL();
  const validBuiltin = await validateBuiltinJobs(builtinJobs());
  return mergeJobs(zcoolJobs, validBuiltin);
}

// ---------- API Handler ----------

module.exports = async function handler(req, res) {
  res.setHeader('Content-Type', 'application/json; charset=utf-8');
  res.setHeader('Access-Control-Allow-Origin', '*');

  // Vercel Cron 触发刷新
  if (req.query.refresh === '1') {
    const jobs = await scrapeAll();
    const data = { jobs, updatedAt: nowBeijing() };
    await saveToBlob(data);
    return res.json({ status: 'ok', jobCount: jobs.length, updatedAt: data.updatedAt });
  }

  // 正常请求：返回岗位数据
  const data = await loadFromBlob();
  if (data) return res.json(data);

  return res.json({ jobs: builtinJobs(), updatedAt: '内置数据（未配置Blob Storage）' });
};
