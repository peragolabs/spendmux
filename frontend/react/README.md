# React frontend shell

**Agents:** canonical SPA playbook → [`../AGENTS-FRONTEND.md`](../AGENTS-FRONTEND.md) (in downloads: `frontend/AGENTS-FRONTEND.md`). Backend → root `AGENTS.md`.

**Humans:** Keller **data-app-marker** routing — backend injects `data-app-id`; `src/main.tsx` mounts one page + `AppLayout`. No client-side router.

Stack: Vite + React 19 + TypeScript + Tailwind CSS v4 + shadcn/ui. Themes via `data-theme` (default `cream-navy`).

```bash
npm install && npm run dev    # Vite → priv/www on build
```

Dev-only: `?page=pricing` (etc.) when `data-app-id` is unset.

Maintainers: source of truth is `templates/shared/spa-frontend/react/base/`.
