# Frontend AI Guidelines (Keller SPA)

**SPA playbook** — page components, page map, nav, themes, Vite dev.

**Backend playbook** → [`../AGENTS.md`](../AGENTS.md) at project root (routes, auth, migrations, JSON APIs, S+S contract).

Read **both** for full features (page + API).

```
router / pages / API  →  data-app-id  →  main.tsx page map  →  Page component
   (root AGENTS.md)                         (this file)
```

Your download includes one SPA folder: `frontend/react`, `frontend/vue`, `frontend/svelte`, or `frontend/solid` (see `FRONTEND` in `docker-compose.yml`).

## Critical rules

1. **No client-side router** — The server picks the page via `data-app-id` on `#app`. Do not add React Router, Vue Router, etc. unless the user explicitly asks.
2. **Page map key = `data-app-id`** — Backend sets the id when serving the HTML shell; this file owns the component map only (not URL registration).
3. **Session cookies** — `fetch('/api/...', { credentials: 'include' })` on every authenticated API call.
4. **Build output** — Vite writes to `priv/www/` (production). Dev uses `npm run dev` with proxy in `vite.config.ts`.

## Entry file by framework

| Framework | Folder | Page map file |
|-----------|--------|---------------|
| React | `frontend/react/` | `src/main.tsx` |
| Vue | `frontend/vue/` | `src/main.ts` |
| Svelte | `frontend/svelte/` | `src/main.ts` |
| Solid | `frontend/solid/` | `src/index.tsx` |

## Page map (default)

| `data-app-id` | Typical route | Component |
|---------------|---------------|-----------|
| `home` | `/` | `LandingPage` |
| `pricing` | `/pricing` | `PricingPage` |
| `login` | `/login` | `LoginPage` |
| `account` | `/account` | `AccountPage` |
| `about` | `/about` | `GettingStartedPage` |
| `admin` | `/admin` | `AdminPage` |
| `notfound` | — | `NotFoundPage` |

Routes are registered on the **backend** (root `AGENTS.md`). The map key must match the id the backend passes to `ServeApp` / `serve_spa_app` / `serve_app`. Unknown ids fall back to `LandingPage` unless the backend serves `notfound`.

## Routing pattern

```typescript
const appDiv = document.getElementById('app')
const appId = appDiv?.dataset.appId ?? 'home'
const Page = pages[appId] ?? LandingPage
```

Backend injects `data-app-id` into the HTML shell (`data-app-marker` split in `index.html`).

## Adding a page (checklist)

**1. Page component** — create `src/pages/DashboardPage.tsx` (`.vue` / `.svelte` / `.tsx` per framework).

**2. Page map** — in the entry file, **above** the `AI-GENERATED PAGES` marker:

```typescript
import DashboardPage from './pages/DashboardPage'
// inside pages = { ... }
dashboard: DashboardPage,
```

**3. Nav (optional but recommended)** — add links in `src/layout/AppLayout.tsx` (or `.vue` / `.svelte`) header and footer:

```tsx
<a href="/dashboard">Dashboard</a>
```

Users can open `/dashboard` via URL once the backend route exists; nav is for discoverability.

**4. Backend** — register `GET /dashboard` and any `/api/...` routes in root **`AGENTS.md`** (page id `dashboard` must match step 2).

**5. API from the page** — example:

```typescript
const res = await fetch('/api/tasks', { credentials: 'include' })
if (res.status === 401) { /* redirect or show login */ }
const data = await res.json()
```

GraphQL stacks: `POST /graphql` with same credentials (mobile may use `Authorization: Bearer` — see root `AGENTS.md`).

## AI append markers

| File | Marker |
|------|--------|
| Entry file (`main.tsx`, `main.ts`, `index.tsx`) | `AI-GENERATED PAGES BELOW` |
| Backend router / pages / migrations | Root `AGENTS.md` |

Append new page map entries **below** the marker comment; do not reorder existing keys.

## Vite dev proxy

`vite.config.ts` proxies API traffic to the backend (default `:8080`):

- `/api`, `/auth`, `/webhooks` — all stacks
- `/graphql` — Absinthe / GraphQL stacks only

```bash
cd frontend/react   # or vue, svelte, solid
npm install && npm run dev
```

## Layout and themes

- **Stack:** Vite (or Next/Angular) + Tailwind CSS v4. React also ships shadcn/ui primitives under `src/components/ui/`.
- **Shell:** `src/layout/AppLayout.*` — header, nav, footer, theme switcher.
- **Themes:** CSS variable packs in `src/index.css`; switch with `<html data-theme="…">` via `ThemeSwitcher` + `localStorage` key `keller:theme`. Defaults to `cream-navy`. Also: `midnight`, `forest`, `aurora`, `plain`.
- **Dev preview:** when `data-app-id` is unset, Vite/Next DEV builds accept `?page=pricing` (etc.) to mount a page.

## Full feature (SPA half)

When root `AGENTS.md` covers migration + routes + API, finish here:

1. `src/pages/TasksPage.tsx` — UI + `fetch('/api/tasks', { credentials: 'include' })`.
2. Entry file — `tasks: TasksPage` above AI marker.
3. `AppLayout.tsx` — optional `<a href="/tasks">Tasks</a>`.

## Do not modify without user request

- `data-app-marker` / `#app` mount contract in `index.html`
- OAuth redirect URLs in frontend (auth is server-driven)
- Shared theme token files unless adding a new theme

## Verify

```bash
# Terminal 1 — backend (see root README)
docker compose up --build

# Terminal 2 — hot reload
cd frontend/react && npm run dev
# open http://localhost:5173/tasks (after backend route + page map exist)
```

Production: rebuild Docker so Vite output lands in `priv/www/`.
