package admin

import (
	"fmt"
	"net/http"
)

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	showError := r.URL.Query().Get("error") == "1"
	errBlock := ""
	if showError {
		errBlock = `<p class="login-error">Invalid username or password.</p>`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, loginPageHTML, errBlock)
}

const loginPageHTML = `<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>hopscotch — sign in</title>
  <link rel="icon" href="data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 36 36'><circle cx='6' cy='26' r='5' fill='%%2338bdf8'/><circle cx='30' cy='26' r='5' fill='%%23818cf8'/><path d='M11 26 Q18 7 25 26' stroke='%%2338bdf8' stroke-width='2.5' stroke-linecap='round' fill='none'/></svg>">
  <style>
    *, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

    body {
      font-family: 'JetBrains Mono', 'Fira Code', ui-monospace, monospace;
      background: #0f172a;
      color: #cbd5e1;
      min-height: 100vh;
      display: flex;
      align-items: center;
      justify-content: center;
    }

    .card {
      background: #1e293b;
      border: 1px solid #334155;
      border-radius: 12px;
      padding: 2.5rem 2.5rem 2rem;
      width: 100%%;
      max-width: 360px;
      display: flex;
      flex-direction: column;
      gap: 1.5rem;
    }

    .brand {
      display: flex;
      align-items: center;
      gap: 0.75rem;
    }
    .brand-wordmark {
      font-size: 1.25rem;
      font-weight: 700;
      color: #f1f5f9;
      letter-spacing: -0.02em;
    }

    h1 {
      font-size: 0.75rem;
      font-weight: 400;
      color: #475569;
      letter-spacing: 0.08em;
      text-transform: uppercase;
    }

    .fields {
      display: flex;
      flex-direction: column;
      gap: 0.75rem;
    }

    label {
      display: flex;
      flex-direction: column;
      gap: 0.35rem;
      font-size: 0.7rem;
      color: #64748b;
      letter-spacing: 0.06em;
      text-transform: uppercase;
    }

    input[type="text"],
    input[type="password"] {
      background: #0f172a;
      border: 1px solid #334155;
      border-radius: 6px;
      color: #f1f5f9;
      font-family: inherit;
      font-size: 0.875rem;
      padding: 0.55rem 0.75rem;
      outline: none;
      transition: border-color 0.15s;
      width: 100%%;
    }
    input:focus {
      border-color: #38bdf8;
    }

    button[type="submit"] {
      background: #38bdf8;
      border: none;
      border-radius: 6px;
      color: #0f172a;
      cursor: pointer;
      font-family: inherit;
      font-size: 0.875rem;
      font-weight: 700;
      padding: 0.65rem 1rem;
      width: 100%%;
      transition: background 0.15s;
      letter-spacing: 0.02em;
    }
    button[type="submit"]:hover { background: #7dd3fc; }

    .login-error {
      font-size: 0.8rem;
      color: #f87171;
      text-align: center;
    }
  </style>
</head>
<body>
  <div class="card">
    <div class="brand">
      <svg width="32" height="32" viewBox="0 0 36 36" fill="none" xmlns="http://www.w3.org/2000/svg" aria-hidden="true">
        <circle cx="6"  cy="26" r="5" fill="#38bdf8" opacity=".9"/>
        <circle cx="30" cy="26" r="5" fill="#818cf8" opacity=".9"/>
        <path d="M11 26 Q18 7 25 26" stroke="url(#g)" stroke-width="2.2" stroke-linecap="round" fill="none"/>
        <circle cx="18" cy="14.5" r="2.6" fill="#38bdf8" opacity=".85"/>
        <defs>
          <linearGradient id="g" x1="11" y1="26" x2="25" y2="26" gradientUnits="userSpaceOnUse">
            <stop offset="0%%"   stop-color="#38bdf8"/>
            <stop offset="100%%" stop-color="#818cf8"/>
          </linearGradient>
        </defs>
      </svg>
      <span class="brand-wordmark">hopscotch</span>
    </div>

    <h1>Admin sign in</h1>

    <form method="POST" action="/api/login" class="fields">
      <label>
        Username
        <input type="text" name="username" autocomplete="username" autofocus required>
      </label>
      <label>
        Password
        <input type="password" name="password" autocomplete="current-password" required>
      </label>
      <button type="submit">Sign in</button>
      %s
    </form>
  </div>
</body>
</html>`
