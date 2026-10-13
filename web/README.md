# Ike web panel

React + TypeScript + Vite. One codebase serves all three roles (server admin, reseller, account owner); the navigation adapts to the signed-in role.

```bash
npm install
npm run dev              # http://localhost:5173, demo data
npm run build            # dist/ — embedded into the control plane
npm run preview:single   # dist-single/index.html — one self-contained file
VITE_IKE_API=http npm run dev   # talk to a real control plane on :8443
```

## Themes

Themes are plain data in `src/theme/themes.ts`: a light and a dark palette plus corner radius and fonts. Every value becomes a CSS custom property, so components never hard-code colors. Add a theme by appending an object (or calling `registerTheme` with one loaded from JSON). Built in: **Uli** (default), **Naija**, **Lagoon**, **Harmattan**, **Terminal**. Each works in light, dark, and "match my device".

## Layout templates

`src/layouts/Shell.tsx` renders the same pages in three layouts:

| Template | Layout |
|---|---|
| Command | Sidebar with grouped sections (default) |
| Horizon | Top navigation, centered content |
| Rail | Icon rail with tooltips, denser tables |

Below 860px wide every template becomes a top bar with bottom tabs and a "More" sheet.

## Data

`src/api/types.ts` mirrors the agent's JSON API. `src/api/demo.ts` is an in-browser implementation with the same validation rules as the agent, used for previews and until the control-plane API ships.
