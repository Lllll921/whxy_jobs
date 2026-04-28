const http = require('http');
const fs = require('fs');
const path = require('path');

const PORT = 3001;

http.createServer((req, res) => {
  if (req.url.startsWith('/api/jobs')) {
    const data = JSON.parse(fs.readFileSync(path.join(__dirname, 'api/data.json'), 'utf-8'));
    res.setHeader('Content-Type', 'application/json; charset=utf-8');
    res.end(JSON.stringify({ jobs: data, updatedAt: '本地预览模式' }));
    return;
  }

  let filePath = path.join(__dirname, 'public', req.url === '/' ? 'index.html' : req.url);
  if (!fs.existsSync(filePath)) { res.writeHead(404); res.end('Not found'); return; }

  const ext = path.extname(filePath);
  const types = { '.html': 'text/html', '.css': 'text/css', '.js': 'text/javascript' };
  res.setHeader('Content-Type', (types[ext] || 'application/octet-stream') + '; charset=utf-8');
  res.end(fs.readFileSync(filePath));
}).listen(PORT, () => console.log(`Local preview: http://localhost:${PORT}`));
