// Safe DOM builder — use instead of innerHTML for any user/server-supplied data.
export function h(tag, attrs, ...children) {
  const el = document.createElement(tag);
  if (attrs) {
    for (const [k, v] of Object.entries(attrs)) {
      if (k.startsWith("on")) continue;
      if ((k === "href" || k === "src") && !/^(https?:|mailto:|\/)/i.test(String(v))) continue;
      el.setAttribute(k, String(v));
    }
  }
  for (const c of children) {
    if (c == null) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

// Global State
export const state = {
  models: [],
  profiles: [],
  servers: [],
  benchmarks: [],
  settings: {},
  activeServerId: null,
  activeProfileIdInLifecycle: null,
  logPollInterval: null,
  statsPollInterval: null,
  listPollInterval: null,
  lastLogCount: 0,
  consoleOpen: false,
  telemetryHistory: { mem: [] },
};

// Auth Queue for requests waiting for token entry
export const authQueue = [];

// Helper for human-readable byte sizes
export function formatBytes(bytes) {
  if (bytes === 0) return "0 Bytes";
  const k = 1024;
  const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
  const i = Math.floor(Math.log(bytes) / Math.log(k));
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
}

// Helper for readable date strings
export function formatDate(isoStr) {
  if (!isoStr) return "-";
  const date = new Date(isoStr);
  return date.toLocaleString();
}

// Helper for human-readable estimated remaining time (ETA)
export function formatETA(secs) {
  if (secs === Infinity || isNaN(secs) || secs < 0) return "—";
  if (secs < 60) return `${Math.round(secs)}s`;
  const mins = Math.floor(secs / 60);
  const remainingSecs = Math.round(secs % 60);
  return `${mins}m ${remainingSecs}s`;
}

let _corsGuideSeen = false;

function showCorsGuide() {
  if (_corsGuideSeen || document.getElementById("cors-guide-banner")) return;
  _corsGuideSeen = true;

  const origin = window.location.origin;   // e.g. "http://192.168.1.42:3100"
  const configPath = "config.json";

  const snippet =
    `{\n` +
    `  ...\n` +
    `  "allowed_origins": ["${origin}"]\n` +
    `}`;

  const banner = h("div", {
    id: "cors-guide-banner",
    role: "alert",
    style: "position:fixed;top:0;left:0;right:0;z-index:9999;" +
      "background:#7f1d1d;color:#fef2f2;" +
      "padding:16px 56px 16px 20px;font-size:0.85rem;line-height:1.6;" +
      "box-shadow:0 3px 12px rgba(0,0,0,.55);"
  });

  banner.append(h("strong", { style: "font-size:0.95rem;display:block;margin-bottom:6px;" },
    "CORS error — browser blocked a cross-origin request"));

  const inlineCode = s => h("code", {
    style: "background:rgba(0,0,0,.35);padding:1px 6px;border-radius:3px;font-size:0.82rem;"
  }, s);

  const explainP = document.createElement("p");
  explainP.style.cssText = "margin:0 0 10px;";
  explainP.append(
    "The browser is loading this page from ",
    inlineCode(origin),
    ", which is not the server’s own origin. " +
    "Add your origin to ",
    inlineCode("allowed_origins"),
    " in ",
    inlineCode(configPath),
    " and restart the server:"
  );
  banner.append(explainP);

  const pre = document.createElement("pre");
  pre.style.cssText =
    "background:rgba(0,0,0,.4);padding:10px 14px;border-radius:6px;" +
    "font-size:0.8rem;margin:0 0 10px;overflow-x:auto;white-space:pre;" +
    "border-left:3px solid rgba(255,255,255,.3);";
  pre.textContent = snippet;
  banner.append(pre);

  const closeBtn = h("button", {
    style: "position:absolute;top:16px;right:20px;background:rgba(255,255,255,.15);" +
      "border:none;color:#fff;font-weight:600;line-height:1;" +
      "padding:4px 12px;border-radius:4px;cursor:pointer;font-size:0.82rem;"
  }, "✕ Dismiss");
  closeBtn.addEventListener("click", () => banner.remove());
  banner.append(closeBtn);

  document.body.prepend(banner);
}

export async function apiCall(url, method = "GET", body = null) {
  try {
    const options = { method, headers: {} };
    const savedToken = localStorage.getItem("admin_token");
    if (savedToken) {
      options.headers["Authorization"] = `Bearer ${savedToken}`;
    }
    if (body) {
      options.headers["Content-Type"] = "application/json";
      options.body = JSON.stringify(body);
    }
    const response = await fetch(url, options);
    if (response.status === 401) {
      localStorage.removeItem("admin_token");

      const overlay = document.getElementById("auth-overlay");
      overlay.style.display = "flex";
      document.getElementById("auth-token-input").focus();

      if (savedToken) {
        document.getElementById("auth-error-msg").style.display = "block";
      } else {
        document.getElementById("auth-error-msg").style.display = "none";
      }

      return new Promise((resolve, reject) => {
        authQueue.push({ url, method, body, resolve, reject });
      });
    }
    if (!response.ok) {
      const errorText = await response.text();
      let parsedErr;
      try {
        parsedErr = JSON.parse(errorText);
      } catch {
        parsedErr = { error: errorText };
      }
      throw new Error(parsedErr.error || `HTTP ${response.status}`);
    }
    if (method === "DELETE") return true;
    return await response.json();
  } catch (err) {
    console.error(`API Call failed (${url}):`, err);
    if (err instanceof TypeError) {
      if (window.location.protocol === "file:") {
        showCorsGuide();
      }
    }
    throw err;
  }
}

export function showModal(title, content) {
  const modal = document.getElementById("info-modal");
  const modalTitle = document.getElementById("modal-title");
  const modalContent = document.getElementById("modal-content");
  modalTitle.textContent = title;
  if (content instanceof Node) {
    modalContent.replaceChildren(content);
  } else {
    modalContent.textContent = String(content ?? "");
  }
  modal.style.display = "flex";
}
