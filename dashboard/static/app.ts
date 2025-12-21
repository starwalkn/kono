// main.ts — TypeScript for dashboard UI (tabs, fetch, render, accordion, filter, refresh, editor)

interface AdminConfig {
  enable: boolean;
  port: number;
}

interface PluginConfig {
  name: string;
  config: Record<string, any>;
}

interface UpstreamConfig {
  url: string;
  method: string;
  timeout?: number;
}

interface RouteConfig {
  path: string;
  method: string;
  backends: UpstreamConfig[];
  plugins: PluginConfig[];
  aggregate: string;
  transform: string;
}

interface ServerConfig {
  port: number;
  timeout: number;
}

interface GatewayConfig {
  schema?: string;
  name?: string;
  version?: string;
  server: ServerConfig;
  admin_panel?: AdminConfig;
  plugins: PluginConfig[];
  routes: RouteConfig[];
}

const CONFIG_URL = "config";
let ALL_ROUTES: RouteConfig[] = [];
declare const CodeMirror: any;
let FULL_CONFIG: GatewayConfig | null = null;
let codeMirrorEditor: any | null = null;

async function fetchConfig(): Promise<GatewayConfig> {
  const resp = await fetch(CONFIG_URL);
  if (!resp.ok) throw new Error(`Config load failed: ${resp.status}`);
  return resp.json();
}

function setVersionInHeader(version?: string) {
  const el = document.getElementById("version-tag");
  const cfgVersionEl = document.getElementById("config-version");
  if (el) el.textContent = version ? `v${version}` : "v?";
  if (cfgVersionEl) cfgVersionEl.textContent = version ? `v${version}` : "v?";
}

function renderServerInfo(cfg: GatewayConfig) {
  const container = document.getElementById("server-info");
  const statusEl = document.getElementById("server-status");
  const server = cfg.server;

  if (!container || !server) return;

  if (statusEl) {
    statusEl.textContent = "● Running";
    statusEl.className = "status-badge status-ok";
  }

  container.innerHTML = `
      <p class="meta"><span class="label">Listen Port</span><span class="value">${server.port}</span></p>
      <p class="meta"><span class="label">Timeout</span><span class="value">${server.timeout} ms</span></p>
      ${cfg.admin_panel?.enable ? `<p class="meta"><span class="label">Admin Port</span><span class="value">${cfg.admin_panel.port}</span></p>` : ""}
    `;
}

function renderPlugins(plugins?: PluginConfig[]) {
  const container = document.getElementById("plugins-list");
  if (!container) return;
  if (!plugins || plugins.length === 0) {
    container.innerHTML = `<div class="glass-card">No global plugins configured.</div>`;
    return;
  }

  container.innerHTML = plugins
    .map(
      (p) => `
    <div class="plugin-item glass-card">
      <div class="plugin-name">${escapeHtml(p.name)}</div>
      <div class="plugin-config"><pre>${escapeHtml(JSON.stringify(p.config, null, 2))}</pre></div>
    </div>
  `,
    )
    .join("");
}

function renderRoutes(routes: RouteConfig[] = []) {
  const container = document.getElementById("routes-list");
  if (!container) return;
  if (routes.length === 0) {
    container.innerHTML = `<div class="glass-card">No routes found matching filter.</div>`;
    return;
  }

  container.innerHTML = routes
    .map((r) => {
      const methodClass = ["GET", "POST", "PUT", "DELETE", "PATCH"].includes(
        r.method.toUpperCase(),
      )
        ? r.method.toUpperCase()
        : "OTHER";

      const numBackends = r.backends?.length || 0;
      const numPlugins = r.plugins?.length || 0;

      const backendsHtml = r.backends
        .map(
          (b) => `
            <div class="backend-method">${escapeHtml(b.method)}</div>
            <div class="backend-url">${escapeHtml(b.url)}</div>
            <div class="backend-timeout">${b.timeout ? `${b.timeout}ms` : "N/A"}</div>
        `,
        )
        .join("");

      const pluginsHtml =
        r.plugins
          ?.map(
            (p) =>
              `
            <div class="route-plugin-item">
                <span class="plugin-name-badge">${escapeHtml(p.name)}</span>
                <span class="plugin-config-compact">${escapeHtml(shortConfig(p.config))}</span>
            </div>
            `,
          )
          .join("") ||
        `<div class="route-plugin-item" style="background: none; border: none; padding:0;"><span class="plugin-config-compact">No plugins attached</span></div>`;

      return `
    <div class="route-card" data-path="${escapeAttr(r.path)}" data-method="${escapeAttr(r.method)}">
      <div class="route-header">
        <div class="left">
          <div class="method ${methodClass}">${escapeHtml(r.method)}</div>
          <div class="path">${escapeHtml(r.path)}</div>
          <span class="meta-badge plugins-count" title="${numPlugins} plugins"><span class="label">🔌</span> ${numPlugins}</span>
          <span class="meta-badge backends-count" title="${numBackends} backends"><span class="label">🔗</span> ${numBackends}</span>
        </div>
        <div class="right"><span class="toggle">▼</span></div>
      </div>
      <div class="route-details">
        <p class="meta"><span class="label">Aggregate:</span> <span class="value">${escapeHtml(r.aggregate)}</span></p>
        <p class="meta"><span class="label">Transform:</span> <span class="value">${escapeHtml(r.transform)}</span></p>

        <div class="meta" style="margin-top: 15px;"><span class="label">Backends:</span></div>
        <div class="backends-grid">
            <div class="header">METHOD</div><div class="header">URL</div><div class="header">TIMEOUT</div>
            ${backendsHtml}
        </div>

        <div class="meta" style="margin-top: 15px;"><span class="label">Plugins:</span></div>
        <div class="route-plugins-list">${pluginsHtml}</div>
      </div>
    </div>`;
    })
    .join("");

  // attach accordion handlers
  container.querySelectorAll<HTMLElement>(".route-card").forEach((card) => {
    const header = card.querySelector<HTMLElement>(".route-header");
    const toggle = card.querySelector<HTMLElement>(".toggle");
    const details = card.querySelector<HTMLElement>(".route-details");
    if (!header || !details) return;

    details.style.maxHeight = "0";
    details.style.opacity = "0";
    details.style.paddingTop = "0";
    details.style.paddingBottom = "0";

    header.addEventListener("click", () => {
      const opened = card.classList.toggle("open");
      if (toggle) toggle.textContent = opened ? "▲" : "▼";

      if (opened) {
        details.style.maxHeight = details.scrollHeight + 30 + "px";
        details.style.opacity = "1";
        details.style.paddingTop = "15px";
        details.style.paddingBottom = "5px";
      } else {
        details.style.maxHeight = details.scrollHeight + 30 + "px";

        details.style.maxHeight = "0";
        details.style.opacity = "0";
        details.style.paddingTop = "0";
        details.style.paddingBottom = "0";
      }
    });
  });
}

// === Configuration Editor Logic ===

function setupConfigEditor(cfg: GatewayConfig) {
  const editorEl = document.getElementById(
    "config-editor",
  ) as HTMLTextAreaElement | null;
  const applyBtn = document.getElementById("apply-config");
  const revertBtn = document.getElementById("revert-config");
  const statusEl = document.getElementById(
    "config-status",
  ) as HTMLParagraphElement | null;

  if (!editorEl || !applyBtn || !revertBtn || !statusEl) {
    console.error("Configuration Editor elements not found in DOM.");
    return;
  }

  if (!codeMirrorEditor) {
    codeMirrorEditor = CodeMirror.fromTextArea(editorEl, {
      mode: "application/json",
      theme: "monokai",
      lineNumbers: true,
      indentUnit: 2,
      tabSize: 2,
      matchBrackets: true,
      autoCloseBrackets: true,
    });
  }

  codeMirrorEditor.setValue(JSON.stringify(cfg, null, 2));
  statusEl.textContent = "";

  setTimeout(() => {
    if (codeMirrorEditor) {
      codeMirrorEditor.setSize("100%", "70vh");
      codeMirrorEditor.refresh();
    }
  }, 50);
}

/* tabs, filter, method filter helpers */
function setupTabs() {
  const buttons =
    document.querySelectorAll<HTMLButtonElement>("aside nav button");
  const sections = document.querySelectorAll<HTMLElement>("main section");
  if (!buttons || buttons.length === 0) return;

  buttons.forEach((btn) => {
    btn.addEventListener("click", () => {
      buttons.forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");

      const target = btn.getAttribute("data-section");
      sections.forEach((s) => s.classList.toggle("visible", s.id === target));

      if (target === "config" && codeMirrorEditor) {
        setTimeout(() => {
          codeMirrorEditor.refresh();
        }, 10);
      }
    });
  });

  const activeSection = document
    .querySelector("nav button.active")
    ?.getAttribute("data-section");
  if (activeSection === "config" && codeMirrorEditor) {
    setTimeout(() => {
      codeMirrorEditor.refresh();
    }, 10);
  }
}

function filterRoutes(methodFilter: string = "ALL", textQuery: string = "") {
  const q = textQuery.trim().toLowerCase();

  let filtered = ALL_ROUTES.filter((r) => {
    const methodMatch =
      methodFilter === "ALL" || r.method.toUpperCase() === methodFilter;
    const textMatch =
      q === "" ||
      r.path.toLowerCase().includes(q) ||
      r.method.toLowerCase().includes(q);
    return methodMatch && textMatch;
  });

  renderRoutes(filtered);
}

function setupFilters() {
  const input = document.getElementById(
    "route-filter",
  ) as HTMLInputElement | null;
  const methodButtons = document.querySelectorAll<HTMLButtonElement>(
    "#method-filter button",
  );
  let currentMethod = "ALL";

  // 1. Text Filter
  if (input) {
    input.addEventListener("input", () => {
      filterRoutes(currentMethod, input.value);
    });
  }

  // 2. Method Filter
  if (methodButtons) {
    methodButtons.forEach((btn) => {
      btn.addEventListener("click", () => {
        methodButtons.forEach((b) => b.classList.remove("active"));
        btn.classList.add("active");
        currentMethod = btn.getAttribute("data-method") || "ALL";
        filterRoutes(currentMethod, input?.value || "");
      });
    });
  }
}

/* helpers */
function escapeHtml(s: unknown): string {
  if (s === null || s === undefined) return "";
  return String(s)
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;")
    .replace(/'/g, "&#039;");
}
function escapeAttr(s: unknown): string {
  return escapeHtml(s).replace(/\s+/g, " ");
}
function shortConfig(cfg: Record<string, any>): string {
  try {
    const entries = Object.entries(cfg || {});
    if (entries.length === 0) return "{}";
    const short = entries
      .slice(0, 3)
      .map(([k, v]) => `${k}: ${String(v)}`)
      .join(", ");
    return entries.length > 3 ? `${short}, ...` : short;
  } catch {
    return "{}";
  }
}

/* init */
document.addEventListener("DOMContentLoaded", async () => {
  setupTabs();
  const refreshBtn = document.getElementById("refresh");
  if (refreshBtn) refreshBtn.addEventListener("click", () => void init());
  await init();
});

async function init() {
  try {
    const cfg = await fetchConfig();
    FULL_CONFIG = cfg;
    setVersionInHeader(cfg.version);
    renderServerInfo(cfg);
    renderPlugins(cfg.plugins);
    setupConfigEditor(cfg);

    // Setup Route data and Filters
    ALL_ROUTES = cfg.routes || [];
    renderRoutes(ALL_ROUTES);
    setupFilters();
  } catch (err) {
    console.error("Admin init failed:", err);
    const main = document.querySelector("main");
    if (main)
      main.innerHTML = `<div style="padding:20px;color:var(--error)" class="glass-card">Failed to load config: ${(err as Error).message}</div>`;
    const statusEl = document.getElementById("server-status");
    if (statusEl) {
      statusEl.textContent = "● Stopped";
      statusEl.className = "status-badge status-error";
    }
    setupConfigEditor({
      server: { port: 0, timeout: 0 },
      plugins: [],
      routes: [],
    });
  }
}
