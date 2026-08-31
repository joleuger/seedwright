package console_code_authorizer

import "html/template"

// ConsolePageTemplate returns the parsed template for the console-code
// display page (reachable from the "More" menu).
func ConsolePageTemplate() *template.Template {
	return template.Must(template.New("console_code").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Console Code — Claim Ownership</title>
    <style>
        *{margin:0;padding:0;box-sizing:border-box}body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:#1a1a2e;color:#e0e0e0;min-height:100vh;display:flex;align-items:center;justify-content:center}
        .card{background:#16213e;border:1px solid #0f3460;border-radius:8px;padding:2.5rem;max-width:480px;width:90%}
        .btn{display:inline-block;background:#e94560;color:#fff;border:none;padding:0.6rem 1.2rem;border-radius:4px;font-size:0.9rem;cursor:pointer;text-decoration:none;margin-top:1rem}
        .btn:hover{background:#c73652}
        .flash{background:#0f3460;border-left:4px solid #53d8fb;padding:0.75rem 1rem;margin-bottom:1.5rem;border-radius:0 4px 4px 0}
        .flash.error{border-left-color:#e94560;background:rgba(233,69,96,0.1)}
        .flash.warn{border-left-color:#f0ad4e;background:rgba(240,173,78,0.1)}
        h1{font-size:1.3rem;margin-bottom:1rem;color:#fff}
        p{color:#a0a0b0;font-size:0.9rem;line-height:1.6;margin-bottom:1rem}
        code{background:#0d1b36;padding:0.3rem 0.6rem;border-radius:4px;font-size:1.1rem;color:#53d8fb;font-weight:bold;letter-spacing:0.15em}
        .code-box{text-align:center;margin:1.5rem 0;padding:1rem;background:#0d1b36;border:1px solid #0f3460;border-radius:6px}
        .back{display:inline-block;margin-top:1rem;color:#a0a0b0;text-decoration:none;font-size:0.85rem}
        .back:hover{color:#e94560}
        .spinner{display:inline-block;width:16px;height:16px;border:2px solid #53d8fb;border-top-color:transparent;border-radius:50%;animation:spin 0.8s linear infinite;margin-right:0.5rem;vertical-align:middle}
        @keyframes spin{to{transform:rotate(360deg)}}
        .timer{font-size:0.8rem;color:#8080a0;margin-top:0.5rem}
    </style>
</head>
<body>
    <div class="card">
        <h1>Console Code</h1>
        <p>The code below was printed to the server console. Enter it on the <a href="/claim-ownership" style="color:#53d8fb">Claim Ownership</a> page to grant yourself Global Admin (Owner).</p>

        <div class="code-box">
            {{ if .ConsoleCode }}
                <code>{{ .ConsoleCode }}</code>
                <div class="timer">Expires in {{ .ExpiresIn }} (single-use)</div>
            {{ else }}
                <span class="spinner"></span> Loading code...
            {{ end }}
        </div>

        {{ if .NoCode }}
            <p>Generate a code from the server console, or wait for one to appear.</p>
        {{ end }}

        {{ if .Message }}
            <div class="flash {{ .MessageClass }}">{{ .Message }}</div>
        {{ end }}

        <a href="#" onclick="regenerate();return false;" class="btn" id="regenBtn">Generate New Code</a>
        <br>
        <a href="{{ .RedirectURL }}" class="back">← Back to Home</a>
    </div>

    <script>
    function regenerate() {
        document.getElementById('regenBtn').textContent = 'Generating...';
        document.getElementById('regenBtn').disabled = true;
        fetch('/{{ .Project }}/console_code/generate', {method:'POST'})
            .then(r => r.json())
            .then(d => {
                document.querySelector('.code-box').innerHTML =
                    '<code>' + d.code + '</code>' +
                    '<div class="timer">Expires in ~10m (single-use)</div>';
                document.getElementById('regenBtn').textContent = 'Generate New Code';
                document.getElementById('regenBtn').disabled = false;
            })
            .catch(() => {
                document.getElementById('regenBtn').textContent = 'Generate New Code';
                document.getElementById('regenBtn').disabled = false;
            });
    }

    // Auto-refresh every 30s to check for a new code.
    setInterval(function() {
        fetch('/{{ .Project }}/console_code')
            .then(r => r.json())
            .then(d => {
                if (d.code) {
                    document.querySelector('.code-box').innerHTML =
                        '<code>' + d.code + '</code>' +
                        '<div class="timer">Expires in ~10m (single-use)</div>';
                }
            })
            .catch(() => {});
    }, 30000);
    </script>
</body>
</html>`))
}
