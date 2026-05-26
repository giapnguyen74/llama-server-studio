/* Llama Server Studio - Main Frontend Application Logic */

import { h, state, apiCall, formatDate, formatBytes, formatETA, showModal } from "./state.js";
import { loadDashboard, loadDashboardSystemMetrics, loadRecentBenchmarksDashboard } from "./dashboard.js";
import { loadModelsTable, populateModelDropdownFilters, filterModels } from "./catalog.js";
import { loadProfilesList, initProfileEditor, selectProfile, updateCLIPreview, updateVRAMEstimate, validateActiveProfile } from "./profiles.js";
import { loadHFHub, stopHFHubPolling } from "./hfhub.js";

// Make functions globally available for inline triggers and lifecycle events
window.createProfileFromModel = function(modelID) {
  document.querySelector("[data-target=profiles]").click();
  setTimeout(() => {
    initProfileEditor();
    const modelSel = document.getElementById("profile-model");
    if (modelSel) {
      modelSel.value = modelID;
    }
    updateCLIPreview();
    updateVRAMEstimate();
  }, 100);
};

window.loadData = loadData;
window.loadDashboard = loadDashboard;
window.loadModelsTable = loadModelsTable;
window.loadProfilesList = loadProfilesList;
window.loadHFHub = loadHFHub;
window.initProfileEditor = initProfileEditor;
window.selectProfile = selectProfile;
window.updateCLIPreview = updateCLIPreview;
window.updateVRAMEstimate = updateVRAMEstimate;

const authQueue = [];

// Auth submit handler
document.getElementById("auth-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const tokenVal = document.getElementById("auth-token-input").value.trim();
  if (!tokenVal) return;

  try {
    const response = await fetch("/api/auth/login", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ password: tokenVal })
    });
    if (!response.ok) {
      document.getElementById("auth-error-msg").textContent = "Invalid credential";
      document.getElementById("auth-error-msg").style.display = "block";
      return;
    }
  } catch (err) {
    document.getElementById("auth-error-msg").textContent = "Login failed: " + err.message;
    document.getElementById("auth-error-msg").style.display = "block";
    return;
  }

  localStorage.setItem("admin_token", "session_active");
  document.getElementById("auth-overlay").style.display = "none";
  document.getElementById("auth-token-input").value = "";

  const queue = [...authQueue];
  authQueue.length = 0;

  for (const req of queue) {
    try {
      const res = await apiCall(req.url, req.method, req.body);
      req.resolve(res);
    } catch (err) {
      req.reject(err);
    }
  }
});

// Selectors
const navButtons = document.querySelectorAll(".nav-btn");
const sections = document.querySelectorAll(".content-section");
const modal = document.getElementById("info-modal");
const modalClose = document.getElementById("btn-close-modal");

// Initial Boot
initNavigation();

// Delegated listener for model-table data-action buttons
document.body.addEventListener("click", (e) => {
  const btn = e.target.closest("[data-action]");
  if (!btn) return;
  const id = btn.dataset.modelId;
  if (!id) return;
  switch (btn.dataset.action) {
    case "build":  window.createProfileFromModel(id); break;
    case "info":   window.viewModelDetails(id); break;
    case "hide":   window.hideModelFromCatalog(id); break;
    case "delete": window.deleteModelFromCatalog(id); break;
  }
});

loadSettings();
loadData();
startGlobalPolling();

// --- 1. NAVIGATION & LAYOUT ---

function initNavigation() {
  navButtons.forEach(btn => {
    btn.addEventListener("click", () => {
      const target = btn.dataset.target;
      
      navButtons.forEach(b => b.classList.remove("active"));
      btn.classList.add("active");

      sections.forEach(sec => sec.classList.remove("active"));
      const activeSec = document.getElementById(`section-${target}`);
      if (activeSec) {
        activeSec.classList.add("active");
      }

      if (target === "dashboard") loadDashboard();
      if (target === "models") loadModelsTable();
      if (target === "hfhub") loadHFHub();
      if (target === "profiles") loadProfilesList();
      if (target === "servers") loadServerLifecycleView();
      if (target === "benchmarks") loadBenchmarksHistory();
      if (target === "security") loadSecurityView();
    });
  });

  if (modalClose) {
    modalClose.addEventListener("click", () => {
      if (modal) modal.style.display = "none";
    });
  }
}

// --- 2. GLOBAL POLLING & API HANDLERS ---

function startGlobalPolling() {
  setInterval(() => {
    // Keep server idle when tab loses focus
    if (document.visibilityState !== "visible") return;

    const activeTab = document.querySelector(".nav-btn.active")?.dataset.target;
    const silent = (activeTab !== "dashboard" && activeTab !== "servers");
    loadData(!silent);
    if (activeTab === "dashboard") {
      loadDashboardSystemMetrics();
    }
  }, 4000);
}

async function loadData(updateUI = true) {
  try {
    const data = await apiCall("/api/state");
    state.models = data.models || [];
    state.profiles = data.profiles || [];
    state.servers = data.servers || [];
    state.benchmarks = data.benchmarks || [];

    const scanBanner = document.getElementById("catalog-scanning-banner");
    if (scanBanner) {
      scanBanner.style.display = data.scanning ? "block" : "none";
    }

    if (updateUI) {
      const activeTab = document.querySelector(".nav-btn.active")?.dataset.target;
      if (activeTab === "dashboard") {
        loadDashboard();
      } else if (activeTab === "servers") {
        renderLifecycleList();
      }
      updateGlobalDiagnosticState();
    }
  } catch (err) {
    console.error("Failed to load initial cache data", err);
  }
}

function updateGlobalDiagnosticState() {
  const statusDot = document.getElementById("global-status-dot");
  const statusText = document.getElementById("global-binary-status");
  
  if (state.settings.llama_server_bin) {
    if (statusDot) statusDot.className = "status-dot green";
    if (statusText) statusText.textContent = "Binary Configured";
  } else {
    if (statusDot) statusDot.className = "status-dot red";
    if (statusText) statusText.textContent = "Setup Needed";
  }
}

// --- 6. SERVER LIFECYCLE SECTION ---

const lcListView   = document.getElementById("lifecycle-list-view");
const lcDetailView = document.getElementById("lifecycle-detail-view");
const lcListBody   = document.getElementById("lifecycle-list-body");

const btnStartSrv   = document.getElementById("btn-start-srv");
const btnStopSrv    = document.getElementById("btn-stop-srv");
const btnRestartSrv = document.getElementById("btn-restart-srv");
const btnBackToList = document.getElementById("btn-back-to-list");

function clearDetailIntervals() {
  if (state.logPollInterval)   { clearInterval(state.logPollInterval);   state.logPollInterval   = null; }
  if (state.statsPollInterval) { clearInterval(state.statsPollInterval); state.statsPollInterval = null; }
}

function openConsole(serverID) {
  state.consoleOpen = true;
  const body    = document.getElementById("server-log-console");
  const closed  = document.getElementById("console-closed-actions");
  const opened  = document.getElementById("console-open-actions");
  if (body)   body.style.display   = "";
  if (closed) closed.style.display = "none";
  if (opened) opened.style.display = "";

  if (serverID) {
    state.lastLogCount = 0;
    if (body) body.replaceChildren(h("div", {class: "terminal-line system-line"}, "[System] Connecting to log stream…"));
    pollServerLogs(serverID);
    if (!state.logPollInterval) {
      state.logPollInterval = setInterval(() => pollServerLogs(serverID), 1000);
    }
  }
}

function closeConsole() {
  state.consoleOpen = false;
  const body    = document.getElementById("server-log-console");
  const closed  = document.getElementById("console-closed-actions");
  const opened  = document.getElementById("console-open-actions");
  if (body)   body.style.display   = "none";
  if (closed) closed.style.display = "";
  if (opened) opened.style.display = "none";
  if (state.logPollInterval) { clearInterval(state.logPollInterval); state.logPollInterval = null; }
}

function clearListInterval() {
  if (state.listPollInterval)  { clearInterval(state.listPollInterval);  state.listPollInterval  = null; }
}

function statusClass(status) {
  if (status === "healthy" || status === "ready")      return "green";
  if (status === "crashed")                            return "red";
  if (status === "starting" || status === "loading")   return "yellow";
  return "gray";
}

const statsCache = {};

async function pollListOnce() {
  try {
    state.servers = await apiCall("/api/servers");

    const running = state.servers.filter(s => s.status !== "stopped");
    await Promise.all(running.map(async srv => {
      try {
        const samples = await apiCall(`/api/servers/${srv.id}/stats`);
        if (samples.length > 0) {
          const last = samples[samples.length - 1];
          const hasMetrics = Array.isArray(srv.profile_snapshot?.args) &&
                             srv.profile_snapshot.args.includes("--metrics");
          statsCache[srv.profile_id] = {
            cpu:    parseFloat(last.cpu_percent || 0).toFixed(1) + "%",
            mem:    (last.memory_rss_bytes / 1024 / 1024 / 1024).toFixed(2) + " GB",
            genTps: hasMetrics ? (last.generation_tokens_per_second || 0).toFixed(1) + " t/s" : "—",
          };
        }
      } catch {}
    }));

    renderLifecycleList();
  } catch {}
}

function renderLifecycleList() {
  if (!lcListBody) return;
  if (state.profiles.length === 0) {
    lcListBody.replaceChildren(
      h("tr", {}, h("td", {colspan: "8", class: "loading-state"}, "No profiles yet — create one in the Profiles tab."))
    );
    return;
  }

  lcListBody.replaceChildren(
    ...state.profiles.map(p => {
      const srv    = state.servers.find(s => s.profile_id === p.id && s.status !== "stopped");
      const status = srv ? srv.status : "stopped";
      const sc     = statsCache[p.id] || {};
      const m      = state.models.find(mod => mod.id === p.model_id);

      let modelLabel = m ? m.display_name : "—";
      if (modelLabel.length > 24) modelLabel = modelLabel.slice(0, 22) + "…";

      const actions = h("div", {class: "row-actions"});
      if (srv) {
        const stopBtn = h("button", {class: "btn btn-sm btn-danger"}, "Stop");
        stopBtn.addEventListener("click", async e => {
          e.stopPropagation();
          stopBtn.disabled = true; stopBtn.textContent = "…";
          try { await apiCall(`/api/servers/${srv.id}/stop`, "POST"); await loadData(); await pollListOnce(); }
          catch (err) { alert(err.message); stopBtn.disabled = false; stopBtn.textContent = "Stop"; }
        });
        const rstBtn = h("button", {class: "btn btn-sm btn-accent"}, "Restart");
        rstBtn.addEventListener("click", async e => {
          e.stopPropagation();
          rstBtn.disabled = true; rstBtn.textContent = "…";
          try { await apiCall(`/api/servers/${srv.id}/restart`, "POST"); await loadData(); await pollListOnce(); }
          catch (err) { alert(err.message); rstBtn.disabled = false; rstBtn.textContent = "Restart"; }
        });
        actions.append(stopBtn, rstBtn);
      } else {
        const startBtn = h("button", {class: "btn btn-sm btn-primary"}, "Start");
        startBtn.addEventListener("click", async e => {
          e.stopPropagation();
          startBtn.disabled = true; startBtn.textContent = "…";
          try { await apiCall(`/api/profiles/${p.id}/start`, "POST"); await loadData(); await pollListOnce(); }
          catch (err) { alert(`Start failed: ${err.message}`); startBtn.disabled = false; startBtn.textContent = "Start"; }
        });
        actions.append(startBtn);
      }

      const tr = h("tr", {},
        h("td", {}, h("strong", {}, p.name)),
        h("td", {class: "cell-model"}, modelLabel),
        h("td", {class: "cell-metric"}, srv ? String(srv.pid || "—") : "—"),
        h("td", {}, h("span", {class: `status-pill ${statusClass(status)}`}, status)),
        h("td", {class: "cell-metric"}, sc.cpu    || "—"),
        h("td", {class: "cell-metric"}, sc.mem    || "—"),
        h("td", {class: "cell-metric"}, sc.genTps || "—"),
        h("td", {}, actions)
      );

      tr.addEventListener("click", () => openProfileDetail(p.id));
      return tr;
    })
  );
}

function loadServerLifecycleView() {
  if (state.activeProfileIdInLifecycle) {
    openProfileDetail(state.activeProfileIdInLifecycle);
    return;
  }
  showListView();
}

function showListView() {
  clearDetailIntervals();
  state.consoleOpen = false;
  if (lcListView) lcListView.style.display   = "";
  if (lcDetailView) lcDetailView.style.display = "none";
  state.activeProfileIdInLifecycle = null;
  state.activeServerId = null;

  renderLifecycleList();
  clearListInterval();
  state.listPollInterval = setInterval(() => {
    if (document.visibilityState === "visible") pollListOnce();
  }, 5000);
  pollListOnce();
}

window.openProfileDetail = function(profileID) {
  state.activeProfileIdInLifecycle = profileID;
  clearListInterval();
  if (lcListView) lcListView.style.display   = "none";
  if (lcDetailView) lcDetailView.style.display = "";

  const p = state.profiles.find(pr => pr.id === profileID);
  const m = p ? state.models.find(mod => mod.id === p.model_id) : null;

  document.getElementById("detail-profile-name").textContent = p ? p.name : profileID;
  document.getElementById("detail-model-name").textContent   = m ? m.display_name : "";

  const gwPort = parseInt(window.location.port || "3100") + 1;
  document.getElementById("stable-route-url").textContent =
    `${window.location.protocol}//${window.location.hostname}:${gwPort}/profiles/${profileID}/v1/completions`;

  const srv = state.servers.find(s => s.profile_id === profileID && s.status !== "stopped");
  enterDetailState(profileID, srv || null);
};

function enterDetailState(profileID, srv) {
  clearDetailIntervals();
  const p = state.profiles.find(pr => pr.id === profileID);

  const BLANK_METRICS = ["metric-prefill-speed","metric-gen-speed","metric-active-slots",
                         "metric-inflight-reqs","metric-queued-reqs","metric-kv-ratio"];

  if (!srv) {
    state.activeServerId = null;
    if (btnStartSrv) btnStartSrv.style.display   = "inline-flex";
    if (btnStopSrv) btnStopSrv.style.display    = "none";
    if (btnRestartSrv) btnRestartSrv.style.display = "none";

    document.getElementById("srv-status-badge").className   = "tel-val status-pill gray";
    document.getElementById("srv-status-badge").textContent = "stopped";
    document.getElementById("srv-pid-val").textContent      = "—";
    document.getElementById("srv-port-val").textContent     = p?.port || "—";
    document.getElementById("srv-uptime-val").textContent   = "—";
    document.getElementById("realtime-cpu").textContent     = "—";
    document.getElementById("realtime-mem").textContent     = "—";
    BLANK_METRICS.forEach(id => { document.getElementById(id).textContent = "—"; });

    if (state.consoleOpen) closeConsole();
  } else {
    state.activeServerId = srv.id;
    state.lastLogCount   = 0;
    if (btnStartSrv) btnStartSrv.style.display   = "none";
    if (btnStopSrv) btnStopSrv.style.display    = "inline-flex";
    if (btnRestartSrv) btnRestartSrv.style.display = "inline-flex";

    const sc = statusClass(srv.status);
    document.getElementById("srv-status-badge").className   = `tel-val status-pill ${sc}`;
    document.getElementById("srv-status-badge").textContent = srv.status;
    document.getElementById("srv-pid-val").textContent      = srv.pid || "—";
    document.getElementById("srv-port-val").textContent     = srv.port || "—";

    pollServerTelemetry(srv.id);
    state.statsPollInterval = setInterval(() => {
      if (document.visibilityState === "visible") pollServerTelemetry(srv.id);
    }, 1500);

    if (state.consoleOpen) {
      openConsole(srv.id);
    }
  }
}

if (btnBackToList) {
  btnBackToList.addEventListener("click", () => showListView());
}

window.selectProfileInLifecycle = function(profileID) {
  openProfileDetail(profileID);
};
window.selectServerInLifecycle = function(serverID) {
  const srv = state.servers.find(s => s.id === serverID);
  if (srv) openProfileDetail(srv.profile_id);
};

async function launchServerInstance(profileID) {
  try {
    await apiCall(`/api/profiles/${profileID}/start`, "POST");
    await loadData();
    state.activeProfileIdInLifecycle = profileID;
    document.querySelector("[data-target=servers]").click();
    setTimeout(() => openProfileDetail(profileID), 200);
  } catch (err) {
    alert(`Start failed: ${err.message}`);
  }
}
window.launchServerInstance = launchServerInstance;

if (btnStartSrv) {
  btnStartSrv.addEventListener("click", async () => {
    const profileID = state.activeProfileIdInLifecycle;
    if (!profileID) return;
    btnStartSrv.disabled = true;
    const tn = [...btnStartSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
    if (tn) tn.textContent = " Starting…";
    try {
      await apiCall(`/api/profiles/${profileID}/start`, "POST");
      await loadData();
      const srv = state.servers.find(s => s.profile_id === profileID && s.status !== "stopped");
      enterDetailState(profileID, srv || null);
    } catch (err) { alert(`Start failed: ${err.message}`); }
    finally {
      btnStartSrv.disabled = false;
      if (tn) tn.textContent = " Start";
    }
  });
}

if (btnStopSrv) {
  btnStopSrv.addEventListener("click", async () => {
    if (!state.activeServerId) return;
    btnStopSrv.disabled = true;
    const tn = [...btnStopSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
    if (tn) tn.textContent = " Stopping…";
    try {
      await apiCall(`/api/servers/${state.activeServerId}/stop`, "POST");
      await loadData();
      const profileID = state.activeProfileIdInLifecycle;
      enterDetailState(profileID, null);
    } catch (err) { alert(`Stop failed: ${err.message}`); }
    finally {
      btnStopSrv.disabled = false;
      if (tn) tn.textContent = " Stop";
    }
  });
}

if (btnRestartSrv) {
  btnRestartSrv.addEventListener("click", async () => {
    if (!state.activeServerId) return;
    btnRestartSrv.disabled = true;
    const tn = [...btnRestartSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim());
    if (tn) tn.textContent = " Restarting…";
    try {
      await apiCall(`/api/servers/${state.activeServerId}/restart`, "POST");
      await loadData();
      const profileID = state.activeProfileIdInLifecycle;
      const srv = state.servers.find(s => s.profile_id === profileID && s.status !== "stopped");
      enterDetailState(profileID, srv || null);
    } catch (err) { alert(`Restart failed: ${err.message}`); }
    finally {
      btnRestartSrv.disabled = false;
      if (tn) tn.textContent = " Restart";
    }
  });
}

const btnToggleConsole = document.getElementById("btn-toggle-console");
if (btnToggleConsole) {
  btnToggleConsole.addEventListener("click", () => {
    if (state.consoleOpen) {
      closeConsole();
    } else {
      openConsole(state.activeServerId);
    }
  });
}

const btnCloseConsole = document.getElementById("btn-close-console");
if (btnCloseConsole) {
  btnCloseConsole.addEventListener("click", () => closeConsole());
}

async function pollServerLogs(serverID) {
  if (document.visibilityState !== "visible") return;
  const consoleDiv = document.getElementById("server-log-console");
  if (!consoleDiv || !state.consoleOpen) return;

  try {
    const lines = await apiCall(`/api/servers/${serverID}/logs?limit=300`);
    if (lines.length > state.lastLogCount) {
      const newLines = lines.slice(state.lastLogCount);
      state.lastLogCount = lines.length;
      
      newLines.forEach(line => {
        const lineEl = document.createElement("div");
        lineEl.className = "terminal-line";
        if (line.includes("[ERROR]") || line.includes("error") || line.includes("failed")) {
          lineEl.classList.add("err-line");
        } else if (line.includes("system") || line.includes("[System]")) {
          lineEl.classList.add("system-line");
        }
        lineEl.textContent = line;
        consoleDiv.appendChild(lineEl);
      });
      consoleDiv.scrollTop = consoleDiv.scrollHeight;
    }
  } catch {}
}

async function pollServerTelemetry(serverID) {
  if (document.visibilityState !== "visible") return;
  try {
    const samples = await apiCall(`/api/servers/${serverID}/stats?limit=30`);
    if (samples.length === 0) return;

    const current = samples[samples.length - 1];

    document.getElementById("realtime-cpu").textContent = parseFloat(current.cpu_percent || 0).toFixed(1) + "%";
    document.getElementById("realtime-mem").textContent = (parseFloat(current.memory_rss_bytes || 0) / 1024 / 1024 / 1024).toFixed(2) + " GB";

    const srv = state.servers.find(s => s.id === serverID);
    const hasMetrics = Array.isArray(srv?.profile_snapshot?.args) && srv.profile_snapshot.args.includes("--metrics");

    if (hasMetrics) {
      document.getElementById("metric-prefill-speed").textContent = parseFloat(current.prompt_tokens_per_second || 0).toFixed(1) + " t/s";
      document.getElementById("metric-gen-speed").textContent     = parseFloat(current.generation_tokens_per_second || 0).toFixed(1) + " t/s";
      document.getElementById("metric-active-slots").textContent   = `${current.busy_slots || 0} / ${current.slot_count || 1}`;
      document.getElementById("metric-inflight-reqs").textContent  = current.requests_processing || "0";
      document.getElementById("metric-queued-reqs").textContent    = current.requests_deferred || "0";

      // Render KV Cache occupancy visually
      const kvPercent = current.ctx_size_observed && current.slot_count
        ? ((current.requests_processing / (current.slot_count * 1024)) * 100).toFixed(1)
        : "0.0";
      document.getElementById("metric-kv-ratio").textContent = kvPercent + "%";
    }

    if (srv && srv.started_at) {
      const start = new Date(srv.started_at).getTime();
      const now = Date.now();
      const diffSecs = Math.max(0, Math.floor((now - start) / 1000));
      
      const hrs = Math.floor(diffSecs / 3600);
      const mins = Math.floor((diffSecs % 3600) / 60);
      const secs = diffSecs % 60;
      document.getElementById("srv-uptime-val").textContent = 
        `${hrs.toString().padStart(2,"0")}:${mins.toString().padStart(2,"0")}:${secs.toString().padStart(2,"0")}`;
    }
  } catch {}
}

const btnDownloadLogs = document.getElementById("btn-download-logs");
if (btnDownloadLogs) {
  btnDownloadLogs.addEventListener("click", () => {
    if (!state.activeServerId) return;
    window.open(`/api/servers/${state.activeServerId}/logs/download`);
  });
}

// --- 7. QUICK PROMPT TEST PLAYGROUND ---

const testPromptText = document.getElementById("test-prompt-text");
const testBtn = document.getElementById("btn-send-inference");
const testOutputBox = document.getElementById("test-output-stream-box");

if (testBtn) {
  testBtn.addEventListener("click", async () => {
    if (!state.activeServerId) {
      alert("Please select a running server tab in Lifecycle to test completions.");
      return;
    }

    const s = state.servers.find(srv => srv.id === state.activeServerId);
    if (!s) return;

    const host = s.host === "0.0.0.0" ? "127.0.0.1" : s.host;
    const prompt = testPromptText.value.trim() || "Hello local llama!";
    const temp = parseFloat(document.getElementById("test-temp").value) || 0.7;
    const tokens = parseInt(document.getElementById("test-tokens").value) || 2048;

    const isStream = document.getElementById("test-stream-enabled").checked;

    const icon = testBtn.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...testBtn.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Generating...";
    testBtn.disabled = true;
    
    testOutputBox.replaceChildren(h("span", {class: "system-line"}, "[Connecting to model completions stream…]"));

    try {
      const response = await fetch(`http://${host}:${s.port}/completion`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          prompt,
          n_predict: tokens,
          temperature: temp,
          stream: isStream
        })
      });

      if (!response.ok) {
        const errTxt = await response.text();
        throw new Error(errTxt || `HTTP ${response.status}`);
      }

      if (isStream) {
        testOutputBox.innerHTML = "";
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop();

          for (const line of lines) {
            const clean = line.trim();
            if (clean.startsWith("data: ")) {
              const dataStr = clean.substring(6);
              if (dataStr === "[DONE]") continue;
              try {
                const parsed = JSON.parse(dataStr);
                const chunk = parsed.content || (parsed.choices && parsed.choices[0].delta.content) || "";
                testOutputBox.appendChild(document.createTextNode(chunk));
                testOutputBox.scrollTop = testOutputBox.scrollHeight;
              } catch {}
            }
          }
        }
      } else {
        const data = await response.json();
        testOutputBox.textContent = data.content || JSON.stringify(data, null, 2);
      }
    } catch (err) {
      testOutputBox.replaceChildren(h("span", {class: "err-line"}, `Request Failed: ${err.message}`));
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Send Inference Request";
      testBtn.disabled = false;
    }
  });
}

const btnCopyCurl = document.getElementById("btn-copy-curl");
if (btnCopyCurl) {
  btnCopyCurl.addEventListener("click", () => {
    if (!state.activeServerId) return;
    const s = state.servers.find(srv => srv.id === state.activeServerId);
    if (!s) return;

    const host = s.host === "0.0.0.0" ? "127.0.0.1" : s.host;
    const prompt = testPromptText.value.trim() || "Hello local llama!";
    const temp = parseFloat(document.getElementById("test-temp").value) || 0.7;
    const tokens = parseInt(document.getElementById("test-tokens").value) || 2048;

    const curl = `curl http://${host}:${s.port}/completion \\\n` +
      `  -H "Content-Type: application/json" \\\n` +
      `  -d '{\n` +
      `    "prompt": "${prompt}",\n` +
      `    "n_predict": ${tokens},\n` +
      `    "temperature": ${temp}\n` +
      `  }'`;

    navigator.clipboard.writeText(curl);
    alert("curl block copied successfully!");
  });
}

// --- 8. BENCHMARKS HISTORY SECTION ---

const btnTriggerBench = document.getElementById("btn-trigger-bench");
if (btnTriggerBench) {
  btnTriggerBench.addEventListener("click", async () => {
    if (!state.activeServerId) {
      alert("Please select a running server to benchmark.");
      return;
    }

    const s = state.servers.find(srv => srv.id === state.activeServerId);
    if (!s) return;

    const promptKey = document.getElementById("bench-prompt").value;
    const repeats = parseInt(document.getElementById("bench-repeats").value) || 3;
    
    let prompt = "Explain quantum computing simply to a child";
    if (promptKey === "quicksort") prompt = "Write quicksort implementation in Go";
    if (promptKey === "poem") prompt = "Write a brief creative poem about a local LLM";

    const icon = btnTriggerBench.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...btnTriggerBench.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Benchmarking...";
    btnTriggerBench.disabled = true;
    
    try {
      alert("Triggering performance benchmark. This can take up to 2 minutes depending on parameters and hardware...");
      await apiCall("/api/benchmarks", "POST", {
        profile_id: s.profile_id,
        prompt,
        max_tokens: 128,
        temperature: 0.2,
        repeats,
        warmups: 1
      });
      alert("Benchmark Completed successfully!");
      document.querySelector("[data-target=benchmarks]").click();
    } catch (err) {
      alert(`Benchmark execution failed: ${err.message}`);
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Execute Performance Run";
      btnTriggerBench.disabled = false;
    }
  });
}

async function loadBenchmarksHistory() {
  const tbody = document.getElementById("benchmarks-table-body");
  if (!tbody) return;

  try {
    state.benchmarks = await apiCall("/api/benchmarks");
    
    if (state.benchmarks.length === 0) {
      tbody.innerHTML = `<tr><td colspan="9" class="empty-state">No benchmark runs recorded.</td></tr>`;
      return;
    }

    tbody.replaceChildren(
      ...state.benchmarks.map(b => {
        const p = state.profiles.find(prof => prof.id === b.profile_id);
        const m = state.models.find(mod => mod.id === b.model_id);

        const profName  = p ? p.name         : "Profile";
        const modelName = m ? m.display_name  : "GGUF Model";
        const speed     = b.result && b.result.avg_tokens_per_sec ? `${parseFloat(b.result.avg_tokens_per_sec).toFixed(2)} T/s` : "-";
        const latency   = b.result && b.result.avg_latency_ms     ? `${parseFloat(b.result.avg_latency_ms).toFixed(0)}ms`       : "-";
        const statusCls = b.status === "completed" ? "green" : "red";

        const deleteBtn = document.createElement("button");
        deleteBtn.className = "btn btn-sm btn-danger";
        deleteBtn.innerHTML = `<svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>`;
        deleteBtn.append(document.createTextNode(" Delete"));
        deleteBtn.addEventListener("click", () => window.deleteBenchmarkRecord(b.id));

        return h("tr", {},
          h("td", {}, h("code", {style: "font-size:0.8rem;"}, b.id.substring(6))),
          h("td", {}, h("strong", {}, profName)),
          h("td", {}, h("span", {style: "font-size:0.8rem; color:var(--text-muted);"}, modelName)),
          h("td", {}, h("span", {style: "font-size:0.8rem; font-style:italic;"}, `"${b.prompt.substring(0, 30)}..."`)),
          h("td", {}, h("strong", {style: "color:var(--accent-pink);"}, speed)),
          h("td", {}, latency),
          h("td", {}, h("span", {class: `status-pill ${statusCls}`}, b.status)),
          h("td", {}, formatDate(b.started_at)),
          h("td", {}, deleteBtn)
        );
      })
    );
  } catch {
    tbody.innerHTML = `<tr><td colspan="9" class="empty-state">Failed to load history metrics.</td></tr>`;
  }
}

window.deleteBenchmarkRecord = async function(runID) {
  if (confirm("Are you sure you want to delete this benchmark record?")) {
    try {
      await apiCall(`/api/benchmarks/${runID}`, "DELETE");
      loadBenchmarksHistory();
    } catch (err) {
      alert(err.message);
    }
  }
};

// --- 9. CONFIGURATION LOAD INIT ---

async function loadSettings() {
  try {
    state.settings = await apiCall("/api/settings");
  } catch (err) {
    console.error("Failed to load settings configuration", err);
  }
}

// --- 10. SECURITY GATEWAY SETTINGS ---

let currentIntegrationTab = "curl";

async function loadSecurityView() {
  await loadSettings();
  updateSecurityStatusBadge(state.settings.gateway_token_set || false);
  
  // Populate default model picker select
  const select = document.getElementById("sec-gateway-default-select");
  if (select) {
    select.innerHTML = '<option value="">— No default (Requires model field) —</option>';
    state.profiles.forEach(p => {
      const opt = document.createElement("option");
      opt.value = p.id;
      opt.textContent = `${p.name} (${p.id})`;
      select.appendChild(opt);
    });
    select.value = state.settings.gateway_default_model || "";
  }

  const activeLabel = document.getElementById("sec-gateway-default-active");
  const statusLabel = document.getElementById("sec-gateway-default-status");
  const toggleBtn = document.getElementById("btn-gateway-default-toggle");
  const healthBtn = document.getElementById("btn-gateway-default-health");

  const activeId = state.settings.gateway_default_model;
  const activeProf = state.profiles.find(p => p.id === activeId);

  if (activeProf) {
    if (activeLabel) activeLabel.textContent = `${activeProf.name} (${activeId})`;

    const runningServer = state.servers.find(s => s.profile_id === activeId && (s.status === "healthy" || s.status === "ready" || s.status === "starting"));
    const isRunning = !!runningServer;

    if (statusLabel) {
      statusLabel.style.display = "";
      statusLabel.textContent = isRunning ? (runningServer.status === "starting" ? "starting" : "ready") : "stopped";
      statusLabel.className = isRunning ? "status-pill green" : "status-pill";
    }

    if (toggleBtn) {
      toggleBtn.style.display = "";
      toggleBtn.textContent = isRunning ? "Stop Server" : "Start Server";
      toggleBtn.className = isRunning ? "btn btn-sm btn-danger" : "btn btn-sm btn-primary";
    }

    if (healthBtn) {
      healthBtn.style.display = isRunning && runningServer.status !== "starting" ? "" : "none";
    }
  } else {
    if (activeLabel) activeLabel.textContent = "None (Required in payload)";
    if (statusLabel) statusLabel.style.display = "none";
    if (toggleBtn) toggleBtn.style.display = "none";
    if (healthBtn) healthBtn.style.display = "none";
  }

  updateCurlExample(state.settings.gateway_token_set || false);
}

function updateSecurityStatusBadge(isSet) {
  const badge = document.getElementById("sec-token-status-badge");
  const btnDisable = document.getElementById("btn-disable-gateway");
  if (badge) {
    badge.textContent = isSet ? "Token set" : "Not set";
    badge.className = isSet ? "status-pill green" : "status-pill";
  }
  if (btnDisable) btnDisable.style.display = isSet ? "inline-flex" : "none";
}

function updateCurlExample(tokenIsSet) {
  const curlBox = document.getElementById("sec-curl-example");
  if (!curlBox) return;
  const activeProfileId = state.profiles.length > 0 ? state.profiles[0].id : "{profile_id}";
  const gwPort = parseInt(window.location.port || "3100") + 1;
  const tokenVal = tokenIsSet ? "<your-gateway-token>" : "sk-xyz";

  if (currentIntegrationTab === "curl") {
    curlBox.textContent =
      `curl -X POST http://${window.location.hostname || "127.0.0.1"}:${gwPort}/v1/chat/completions \\\n` +
      `  -H "Authorization: Bearer ${tokenVal}" \\\n` +
      `  -H "Content-Type: application/json" \\\n` +
      `  -d '{\n` +
      `    "model": "${activeProfileId}",\n` +
      `    "messages": [{"role": "user", "content": "Hello!"}]\n` +
      `  }'`;
  } else {
    curlBox.textContent =
      `from openai import OpenAI\n\n` +
      `client = OpenAI(\n` +
      `    base_url="http://${window.location.hostname || "127.0.0.1"}:${gwPort}/v1",\n` +
      `    api_key="${tokenVal}",\n` +
      `)\n\n` +
      `response = client.chat.completions.create(\n` +
      `    model="${activeProfileId}",\n` +
      `    messages=[{"role": "user", "content": "Hello!"}],\n` +
      `)\n` +
      `print(response.choices[0].message.content)`;
  }
}

function generateToken() {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  let suffix = "";
  for (let i = 0; i < 32; i++) suffix += chars.charAt(Math.floor(Math.random() * chars.length));
  return `sk-${suffix}`;
}

const btnGenToken = document.getElementById("btn-gen-sec-token");
if (btnGenToken) {
  btnGenToken.addEventListener("click", async () => {
    btnGenToken.disabled = true;
    const originalText = btnGenToken.textContent;
    btnGenToken.textContent = " Generating…";

    const freshToken = generateToken();

    try {
      const res = await apiCall("/api/settings/security", "POST", { gateway_token: freshToken });
      const isSet = res.gateway_token_set || false;

      state.settings.gateway_token_set = isSet;
      updateSecurityStatusBadge(isSet);
      updateCurlExample(isSet);

      if (isSet) {
        const banner = document.getElementById("sec-copy-banner");
        const tokenDisplay = document.getElementById("sec-copy-token-val");
        if (banner && tokenDisplay) {
          tokenDisplay.textContent = freshToken;
          banner.style.display = "block";
        }
      }
    } catch (err) {
      alert("Failed to generate gateway token: " + err.message);
    } finally {
      btnGenToken.textContent = originalText;
      btnGenToken.disabled = false;
    }
  });
}

const btnDisableGateway = document.getElementById("btn-disable-gateway");
if (btnDisableGateway) {
  btnDisableGateway.addEventListener("click", async () => {
    if (!confirm("Disable the gateway? All clients using the current token will lose access.")) return;
    btnDisableGateway.disabled = true;

    try {
      const res = await apiCall("/api/settings/security", "POST", { gateway_token: "" });
      const isSet = res.gateway_token_set || false;

      state.settings.gateway_token_set = isSet;
      updateSecurityStatusBadge(isSet);
      updateCurlExample(isSet);

      const banner = document.getElementById("sec-copy-banner");
      if (banner) {
        banner.style.display = "none";
        const tokenDisplay = document.getElementById("sec-copy-token-val");
        if (tokenDisplay) tokenDisplay.textContent = "";
      }
    } catch (err) {
      alert("Failed to disable gateway: " + err.message);
    } finally {
      btnDisableGateway.disabled = false;
    }
  });
}

const btnCopyNewToken = document.getElementById("btn-copy-new-token");
if (btnCopyNewToken) {
  btnCopyNewToken.addEventListener("click", () => {
    const val = document.getElementById("sec-copy-token-val")?.textContent || "";
    navigator.clipboard.writeText(val).then(() => {
      btnCopyNewToken.textContent = "Copied!";
      setTimeout(() => { btnCopyNewToken.textContent = "Copy to clipboard"; }, 2000);
    });
  });
}

const btnDismissBanner = document.getElementById("btn-dismiss-copy-banner");
if (btnDismissBanner) {
  btnDismissBanner.addEventListener("click", () => {
    const banner = document.getElementById("sec-copy-banner");
    if (banner) {
      banner.style.display = "none";
      const tokenDisplay = document.getElementById("sec-copy-token-val");
      if (tokenDisplay) tokenDisplay.textContent = "";
    }
  });
}

// Default Model Save Handler
const btnSaveGatewayDefault = document.getElementById("btn-save-gateway-default");
if (btnSaveGatewayDefault) {
  btnSaveGatewayDefault.addEventListener("click", async () => {
    const select = document.getElementById("sec-gateway-default-select");
    if (!select) return;
    btnSaveGatewayDefault.disabled = true;
    const profileID = select.value;

    try {
      const res = await apiCall("/api/settings/gateway-default", "POST", { profile_id: profileID });
      state.settings.gateway_default_model = res.gateway_default_model || "";
      
      const activeLabel = document.getElementById("sec-gateway-default-active");
      if (activeLabel) {
        const activeId = state.settings.gateway_default_model;
        const activeProf = state.profiles.find(p => p.id === activeId);
        activeLabel.textContent = activeProf ? `${activeProf.name} (${activeId})` : "None (Required in payload)";
      }
      alert("Gateway default model updated successfully.");
    } catch (err) {
      alert("Failed to update gateway default: " + err.message);
    } finally {
      btnSaveGatewayDefault.disabled = false;
    }
  });
}

// Integration Example Tabs Handler
const btnIntcURL = document.getElementById("btn-integration-curl");
const btnIntPython = document.getElementById("btn-integration-python");

if (btnIntcURL && btnIntPython) {
  btnIntcURL.addEventListener("click", () => {
    currentIntegrationTab = "curl";
    btnIntcURL.classList.add("active");
    btnIntPython.classList.remove("active");
    updateCurlExample(state.settings.gateway_token_set || false);
  });

  btnIntPython.addEventListener("click", () => {
    currentIntegrationTab = "python";
    btnIntPython.classList.add("active");
    btnIntcURL.classList.remove("active");
    updateCurlExample(state.settings.gateway_token_set || false);
  });
}

// Default Model Start/Stop Toggle Handler
const btnDefaultToggle = document.getElementById("btn-gateway-default-toggle");
if (btnDefaultToggle) {
  btnDefaultToggle.addEventListener("click", async () => {
    const activeId = state.settings.gateway_default_model;
    if (!activeId) return;

    btnDefaultToggle.disabled = true;
    const runningServer = state.servers.find(s => s.profile_id === activeId && (s.status === "healthy" || s.status === "ready" || s.status === "starting"));

    try {
      if (runningServer) {
        btnDefaultToggle.textContent = "Stopping...";
        await apiCall(`/api/servers/${runningServer.id}/stop`, "POST");
      } else {
        btnDefaultToggle.textContent = "Starting...";
        await apiCall(`/api/profiles/${activeId}/start`, "POST");
      }
      // Reload and refresh
      await loadData();
      await loadSecurityView();
    } catch (err) {
      alert("Action failed: " + err.message);
    } finally {
      btnDefaultToggle.disabled = false;
    }
  });
}

// Default Model Health Check completions ping test
const btnDefaultHealth = document.getElementById("btn-gateway-default-health");
if (btnDefaultHealth) {
  btnDefaultHealth.addEventListener("click", async () => {
    const activeId = state.settings.gateway_default_model;
    if (!activeId) return;

    const runningServer = state.servers.find(s => s.profile_id === activeId && (s.status === "healthy" || s.status === "ready"));
    if (!runningServer) {
      alert("No active server running for this default profile to perform health check.");
      return;
    }

    btnDefaultHealth.disabled = true;
    btnDefaultHealth.textContent = "Checking...";

    const startTime = performance.now();
    try {
      const res = await apiCall(`/api/servers/${runningServer.id}/test`, "POST", {
        prompt: "Say 'OK'",
        temp: 0.1,
        max_tokens: 5,
        stream: false
      });
      const latency = Math.round(performance.now() - startTime);
      alert(`✅ Health Check Success!\nLatency: ${latency} ms\nResponse: "${res.content || ""}"`);
    } catch (err) {
      alert("❌ Health Check Failed: " + err.message);
    } finally {
      btnDefaultHealth.disabled = false;
      btnDefaultHealth.textContent = "Health Check";
    }
  });
}
