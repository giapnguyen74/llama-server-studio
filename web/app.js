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
      if (target === "benchmarks") {
        loadBenchmarksForm();
        loadBenchmarksHistory();
      }
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
        // Always re-render the list (even if hidden) so status pills stay fresh.
        renderLifecycleList();
        // If the detail view is open, sync the status badge from the fresh server list.
        if (state.activeServerId) {
          const freshSrv = state.servers.find(s => s.id === state.activeServerId);
          if (freshSrv) syncDetailStatusBadge(freshSrv);
        }
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
  
  if (!statusDot || !statusText) return;

  if (!state.settings || !state.settings.llama_server_bin) {
    statusDot.className = "status-dot red";
    statusText.textContent = "Binary Missing";
    return;
  }

  // 1. Active benchmarking runs
  const isBenchmarking = state.benchmarks && state.benchmarks.some(b => b.status === "running");
  if (isBenchmarking) {
    statusDot.className = "status-dot yellow";
    statusText.textContent = "Benchmarking";
    return;
  }

  // 2. Servers starting or loading
  const startingCount = state.servers ? state.servers.filter(s => s.status === "starting" || s.status === "loading").length : 0;
  if (startingCount > 0) {
    statusDot.className = "status-dot yellow";
    statusText.textContent = `Starting ${startingCount} Server${startingCount > 1 ? "s" : ""}...`;
    return;
  }

  // 3. Active servers running/ready
  const activeCount = state.servers ? state.servers.filter(s => s.status === "healthy" || s.status === "ready").length : 0;
  if (activeCount > 0) {
    statusDot.className = "status-dot green";
    statusText.textContent = `Active: ${activeCount} Server${activeCount > 1 ? "s" : ""}`;
    return;
  }

  // 4. Default System Ready
  statusDot.className = "status-dot green";
  statusText.textContent = "System Ready";
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
          const tps = last.generation_tokens_per_second || last.tokens_per_second || 0;
          statsCache[srv.profile_id] = {
            genTps: tps > 0 ? tps.toFixed(1) + " t/s" : "0.0 t/s",
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
      h("tr", {}, h("td", {colspan: "7", class: "loading-state"}, "No profiles yet — create one in the Profiles tab."))
    );
    return;
  }

  const sortedProfiles = [...state.profiles].sort((a, b) => a.name.localeCompare(b.name));

  lcListBody.replaceChildren(
    ...sortedProfiles.map(p => {
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

      // Calculate capabilities dynamically based on arguments list
      const argsList = p.args || [];
      const caps = [];
      if (argsList.includes("-m") || argsList.includes("--model") || argsList.length > 0) {
        caps.push({ text: "TXT", color: "purple" });
      }
      if (argsList.includes("--embeddings")) {
        caps.push({ text: "EMB", color: "green" });
      }
      const hasMMProj = argsList.some((a, idx) => a === "--mmproj" && idx + 1 < argsList.length && argsList[idx+1] !== "");
      if (hasMMProj) {
        caps.push({ text: "VIS", color: "cyan" });
      }
      if (caps.length === 0) {
        caps.push({ text: "TXT", color: "purple" });
      }

      const capsSpanCell = h("td", {});
      caps.forEach(cap => {
        capsSpanCell.append(h("span", {
          class: `status-pill ${cap.color}`,
          style: "font-size: 0.68rem; font-weight: 800; text-transform: uppercase; padding: 2px 6px; border-radius: 4px; letter-spacing: 0.05em; margin-right: 4px;"
        }, cap.text), " ");
      });

      const tr = h("tr", {},
        h("td", {}, h("strong", {}, p.name)),
        h("td", {class: "cell-model"}, modelLabel),
        capsSpanCell,
        h("td", {class: "cell-metric"}, srv ? String(srv.pid || "—") : "—"),
        h("td", {}, h("span", {class: `status-pill ${statusClass(status)}`}, status)),
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

  // Hide attachment in quick test for non-multimodal server
  const isMultimodal = p && Array.isArray(p.args) && p.args.some((a, idx) => a === "--mmproj" && idx + 1 < p.args.length && p.args[idx+1] !== "");
  const attachmentGroup = document.getElementById("quick-test-attachment-group");
  if (attachmentGroup) {
    attachmentGroup.style.display = isMultimodal ? "block" : "none";
  }

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

const btnViewConsole = document.getElementById("btn-view-console");
if (btnViewConsole) {
  btnViewConsole.addEventListener("click", (e) => {
    e.stopPropagation();
    openConsole(state.activeServerId);
  });
}

const consoleToggleBar = document.getElementById("console-toggle-bar");
if (consoleToggleBar) {
  consoleToggleBar.addEventListener("click", (e) => {
    if (e.target.closest(".term-actions")) return;
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

// Sync the detail-view status badge (and PID) from a live server object.
// Called from pollServerTelemetry (every 1.5s) and from loadData (every 4s)
// so the badge always reflects the actual process state.
function syncDetailStatusBadge(srv) {
  const badge = document.getElementById("srv-status-badge");
  if (!badge) return;
  const sc = statusClass(srv.status);
  badge.className   = `tel-val status-pill ${sc}`;
  badge.textContent = srv.status;
  const pidEl = document.getElementById("srv-pid-val");
  if (pidEl) pidEl.textContent = srv.pid || "—";
}

async function pollServerTelemetry(serverID) {
  if (document.visibilityState !== "visible") return;
  try {
    // Re-fetch the server record so status transitions (starting → healthy)
    // are reflected immediately without waiting for the 4-second global poll.
    const freshSrv = await apiCall(`/api/servers/${serverID}`);
    if (freshSrv) {
      // Patch state.servers in-place so the rest of the UI stays consistent.
      const idx = state.servers.findIndex(s => s.id === serverID);
      if (idx !== -1) state.servers[idx] = freshSrv;
      syncDetailStatusBadge(freshSrv);
    }

    const samples = await apiCall(`/api/servers/${serverID}/stats?limit=30`);
    if (samples.length === 0) return;

    const current = samples[samples.length - 1];

    const srv = state.servers.find(s => s.id === serverID);
    const hasMetrics = Array.isArray(srv?.profile_snapshot?.args) && srv.profile_snapshot.args.includes("--metrics");

    if (hasMetrics) {
      document.getElementById("metric-prefill-speed").textContent = parseFloat(current.prompt_tokens_per_second || 0).toFixed(1) + " t/s";
      document.getElementById("metric-gen-speed").textContent     = parseFloat(current.generation_tokens_per_second || 0).toFixed(1) + " t/s";
      document.getElementById("metric-active-slots").textContent   = `${current.busy_slots || 0} / ${current.slot_count || 1}`;
      document.getElementById("metric-inflight-reqs").textContent  = current.requests_processing || "0";
      document.getElementById("metric-queued-reqs").textContent    = current.requests_deferred || "0";

      // Render KV Cache occupancy visually
      let kvPercent = "0.0";
      if (current.ctx_size_observed) {
        let ctxLimit = 8192;
        if (srv?.profile_snapshot?.args) {
          const args = srv.profile_snapshot.args;
          for (let i = 0; i < args.length; i++) {
            if (args[i] === "-c" && i + 1 < args.length) {
              const val = parseInt(args[i+1]);
              if (!isNaN(val)) ctxLimit = val;
            }
          }
        }
        kvPercent = ((current.ctx_size_observed / ctxLimit) * 100).toFixed(1);
      }
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
// --- 7. QUICK PROMPT TEST PLAYGROUND & MULTIMODAL INFERENCE ---

const testPromptText = document.getElementById("test-prompt");
const testBtn = document.getElementById("btn-test-srv");
const testOutputBox = document.getElementById("test-response-output");

// Dynamic attachment state
let attachedBase64 = null;
let attachedFile = null;

// File Upload Bindings
const testAttachmentInput = document.getElementById("test-attachment");
const btnTriggerUpload = document.getElementById("btn-trigger-upload");
const attachmentPreview = document.getElementById("attachment-preview-container");
const attachmentName = document.getElementById("attachment-name");
const btnClearAttachment = document.getElementById("btn-clear-attachment");

if (btnTriggerUpload && testAttachmentInput) {
  btnTriggerUpload.addEventListener("click", () => {
    testAttachmentInput.click();
  });
}

if (testAttachmentInput) {
  testAttachmentInput.addEventListener("change", (e) => {
    const file = e.target.files[0];
    if (!file) return;

    // Enforce 10MB limit
    if (file.size > 10 * 1024 * 1024) {
      alert("Attachment exceeds the 10MB limit. Please upload a smaller image or audio clip.");
      testAttachmentInput.value = "";
      return;
    }

    const reader = new FileReader();
    reader.onload = () => {
      attachedBase64 = reader.result;
      attachedFile = file;
      if (attachmentName) attachmentName.textContent = file.name;
      if (attachmentPreview) attachmentPreview.style.display = "inline-flex";
    };
    reader.onerror = () => {
      alert("Failed to read file. Please try again.");
    };
    reader.readAsDataURL(file);
  });
}

if (btnClearAttachment) {
  btnClearAttachment.addEventListener("click", () => {
    attachedBase64 = null;
    attachedFile = null;
    if (testAttachmentInput) testAttachmentInput.value = "";
    if (attachmentPreview) attachmentPreview.style.display = "none";
  });
}

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
    const temp = 0.7;
    const tokens = 2048;

    const isStream = true;

    const icon = testBtn.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...testBtn.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Generating...";
    testBtn.disabled = true;
    
    testOutputBox.replaceChildren(h("span", {class: "system-line"}, "[Connecting to model completions stream…]"));

    try {
      const payload = {
        prompt,
        max_tokens: tokens,
        temperature: temp,
        stream: isStream
      };

      if (attachedFile) {
        const dataURL = attachedBase64;
        const commaIdx = dataURL.indexOf(",");
        const raw = dataURL.slice(commaIdx + 1);

        // Sanitize File.type: lowercase, strip params (e.g. "; charset=utf-8"),
        // then allow only the characters that belong in a MIME type token.
        // This prevents a crafted browser MIME from injecting into the data URL
        // or the JSON payload on the backend.
        const rawMime = (attachedFile.type || "").trim().toLowerCase();
        const baseMime = rawMime.split(";")[0].trim(); // drop any parameters
        const safeMimeRe = /^[a-z0-9][a-z0-9!#$&\-^_+.]*\/[a-z0-9][a-z0-9!#$&\-^_+.]*$/;
        if (!safeMimeRe.test(baseMime)) {
          throw new Error("Attachment has an unrecognised or unsafe MIME type: " + (rawMime || "(empty)"));
        }
        const mime = baseMime;

        let kind;
        if (mime.startsWith("image/")) kind = "image";
        else if (mime.startsWith("audio/")) kind = "audio";
        else throw new Error("Unsupported file type: " + mime);
        payload.attachment = { kind, mime, data: raw };
      }

      const savedToken = localStorage.getItem("admin_token");
      const headers = { "Content-Type": "application/json" };
      if (savedToken) {
        headers["Authorization"] = `Bearer ${savedToken}`;
      }

      const response = await fetch(`/api/servers/${state.activeServerId}/test`, {
        method: "POST",
        headers: headers,
        body: JSON.stringify(payload)
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
        let currentEvent = null;

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop();

          for (const line of lines) {
            const clean = line.trim();
            if (clean.startsWith("event: ")) {
              currentEvent = clean.substring(7).trim();
            } else if (clean.startsWith("data: ")) {
              const dataStr = clean.substring(6);
              if (dataStr === "[DONE]") continue;
              try {
                const parsed = JSON.parse(dataStr);
                if (currentEvent === "error") {
                  testOutputBox.replaceChildren(h("span", {class: "err-line"}, `Request Failed: [${parsed.status}] ${parsed.message}`));
                  currentEvent = null;
                  continue;
                }
                const chunk = (parsed.choices && parsed.choices[0] && parsed.choices[0].delta && parsed.choices[0].delta.content) || "";
                testOutputBox.appendChild(document.createTextNode(chunk));
                testOutputBox.scrollTop = testOutputBox.scrollHeight;
              } catch {}
              currentEvent = null;
            }
          }
        }
      } else {
        const data = await response.json();
        const responseText = (data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content) || data.content || JSON.stringify(data, null, 2);
        testOutputBox.textContent = responseText;
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
    const temp = 0.7;
    const tokens = 2048;

    let messages = [
      {
        "role": "user",
        "content": [
          { "type": "text", "text": prompt }
        ]
      }
    ];

    if (attachedFile && attachedBase64) {
      const dataURL = attachedBase64;
      const commaIdx = dataURL.indexOf(",");
      const raw = dataURL.slice(commaIdx + 1);
      const mime = attachedFile.type || "";
      const truncated = raw.substring(0, 40) + "...";
      if (mime.startsWith("image/")) {
        messages[0].content.push({
          "type": "image_url",
          "image_url": { "url": `data:${mime};base64,${truncated}` }
        });
      } else if (mime.startsWith("audio/")) {
        let fmtVal = "wav";
        if (mime.includes("mpeg") || mime.includes("mp3")) fmtVal = "mp3";
        else if (mime.includes("flac")) fmtVal = "flac";
        else if (mime.includes("ogg")) fmtVal = "ogg";
        messages[0].content.push({
          "type": "input_audio",
          "input_audio": { "data": truncated, "format": fmtVal }
        });
      }
    }

    const payload = {
      "model": s.profile_snapshot?.name || "model",
      "messages": messages,
      "max_tokens": tokens,
      "temperature": temp,
      "stream": false
    };

    const curl = `curl -X POST http://${host}:${s.port}/v1/chat/completions \\\n` +
      `  -H "Content-Type: application/json" \\\n` +
      `  -d '${JSON.stringify(payload, null, 2)}'`;

    navigator.clipboard.writeText(curl);
    alert("curl block copied successfully!");
  });
}

// --- 8. BENCHMARKS HISTORY & REDESIGN SUITE ---

async function loadBenchmarksForm() {
  const profileSelect = document.getElementById("bench-profile");
  if (profileSelect) {
    profileSelect.replaceChildren(
      ...state.profiles.map(p => h("option", {value: p.id}, p.name))
    );
  }

  const wlSelect = document.getElementById("bench-workload");
  if (wlSelect && (!wlSelect.children || wlSelect.children.length <= 1)) {
    try {
      const workloads = await apiCall("/api/benchmarks/corpus");
      wlSelect.replaceChildren(
        ...workloads.map(w => h("option", {value: w.id}, `${w.name} (${w.max_tokens} tokens)`))
      );
    } catch {}
  }
}

const benchTypeSelect = document.getElementById("bench-type");
if (benchTypeSelect) {
  benchTypeSelect.addEventListener("change", () => {
    const kind = benchTypeSelect.value;
    document.getElementById("bench-sweep-options").style.display = kind === "sweep" ? "grid" : "none";
    document.getElementById("bench-concurrency-options").style.display = kind === "concurrency" ? "grid" : "none";
  });
}

const btnTriggerBench = document.getElementById("btn-trigger-bench");
if (btnTriggerBench) {
  btnTriggerBench.addEventListener("click", async () => {
    const profileID = document.getElementById("bench-profile").value;
    if (!profileID) {
      alert("Please select a profile to test.");
      return;
    }

    const kind = document.getElementById("bench-type").value;
    const workloadID = document.getElementById("bench-workload").value;
    
    let sweepFlag = "";
    let sweepValues = [];
    if (kind === "sweep") {
      sweepFlag = document.getElementById("bench-sweep-flag").value;
      const valStr = document.getElementById("bench-sweep-values").value.trim();
      sweepValues = valStr.split(",").map(v => v.trim()).filter(Boolean);
      if (sweepValues.length === 0) {
        alert("Please enter at least one value to sweep.");
        return;
      }
    }

    let concurrencyPlan = [];
    if (kind === "concurrency") {
      const valStr = document.getElementById("bench-concurrency-plan").value.trim();
      concurrencyPlan = valStr.split(",").map(v => parseInt(v.trim())).filter(n => !isNaN(n));
      if (concurrencyPlan.length === 0) {
        concurrencyPlan = [1, 2, 4];
      }
    }

    const icon = btnTriggerBench.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...btnTriggerBench.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Spawning Suite...";
    btnTriggerBench.disabled = true;

    try {
      alert("Performance benchmark suite triggered successfully! Fresh sandbox servers will be spawned sequentially cell-by-cell. You can monitor the history table.");
      await apiCall("/api/benchmarks", "POST", {
        profile_id: profileID,
        kind,
        sweep_flag: sweepFlag,
        sweep_values: sweepValues,
        workload_id: workloadID,
        concurrency_plan: concurrencyPlan,
        repeats: 5,
        warmups: 2
      });
      
      // Auto-poll history after a short delay
      setTimeout(loadBenchmarksHistory, 1500);
      setInterval(loadBenchmarksHistory, 6000);
    } catch (err) {
      alert(`Benchmark execution failed: ${err.message}`);
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Execute Performance Suite";
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

    // Auto-update latest completed recommendation card
    const completed = state.benchmarks.filter(b => b.status === "completed" && b.recommendation);
    const recBox = document.getElementById("bench-recommendation-box");
    if (completed.length > 0 && recBox) {
      recBox.style.display = "block";
      document.getElementById("bench-recommendation-text").textContent = completed[completed.length - 1].recommendation;
    } else if (recBox) {
      recBox.style.display = "none";
    }

    const sortedBenchmarks = [...state.benchmarks].sort((a, b) => b.id.localeCompare(a.id));

    tbody.replaceChildren(
      ...sortedBenchmarks.map(b => {
        const p = state.profiles.find(prof => prof.id === b.profile_id);
        const profName  = p ? p.name : "Profile";

        let typeLabel = "Single-Shot";
        if (b.kind === "sweep") typeLabel = `Sweep (${b.sweep_flag})`;
        if (b.kind === "concurrency") typeLabel = "Concurrency Load";

        let speed = "-";
        let latency = "-";
        if (b.cells && b.cells.length > 0) {
          // Display optimal completed cell or first cell details
          const first = b.cells[0];
          speed = `${parseFloat(first.aggregates.tg_speed_mean || 0).toFixed(1)} t/s`;
          latency = `${parseFloat(first.aggregates.e2e_p50 || 0).toFixed(0)}ms`;
        } else if (b.result) {
          speed = b.result.avg_tokens_per_sec ? `${parseFloat(b.result.avg_tokens_per_sec).toFixed(1)} t/s` : "-";
          latency = b.result.avg_latency_ms ? `${parseFloat(b.result.avg_latency_ms).toFixed(0)}ms` : "-";
        }

        const deleteBtn = document.createElement("button");
        deleteBtn.className = "btn btn-sm btn-danger";
        deleteBtn.innerHTML = `<svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>`;
        deleteBtn.addEventListener("click", (e) => {
          e.stopPropagation();
          window.deleteBenchmarkRecord(b.id);
        });

        const tr = h("tr", {style: "cursor:pointer;"},
          h("td", {}, h("code", {style: "font-size:0.8rem;"}, b.id.substring(6))),
          h("td", {}, 
            h("strong", {}, profName),
            b.error ? h("div", {style: "font-size:0.72rem; color:#f87171; margin-top:3px; font-weight:500;"}, `Error: ${b.error}`) : null
          ),
          h("td", {}, h("span", {class: "status-pill yellow", style: "font-size:0.75rem;"}, typeLabel)),
          h("td", {}, h("span", {style: "font-size:0.8rem; font-style:italic;"}, b.workload_id || "custom")),
          h("td", {}, h("strong", {style: "color:var(--accent-pink);"}, speed)),
          h("td", {}, latency),
          h("td", {}, h("span", {class: `status-pill ${b.status === "completed" ? "green" : (b.status === "running" ? "yellow" : "red")}`}, b.status)),
          h("td", {}, formatDate(b.started_at)),
          h("td", {}, deleteBtn)
        );

        tr.addEventListener("click", () => window.viewBenchmarkDetails(b.id));
        return tr;
      })
    );
  } catch (err) {
    tbody.innerHTML = `<tr><td colspan="9" class="empty-state">Failed to load history metrics.</td></tr>`;
  }
}

window.viewBenchmarkDetails = function(runID) {
  const b = state.benchmarks.find(run => run.id === runID);
  if (!b) return;
  
  const frag = document.createDocumentFragment();
  if (b.recommendation) {
    frag.append(h("div", {
      style: "padding:12px; margin-bottom:16px; background:rgba(16,163,127,0.08); border:1px solid rgba(16,163,127,0.2); border-radius:6px; font-size:0.85rem; font-weight:500; color:var(--text-main);"
    }, b.recommendation));
  }

  if (b.status === "failed" || b.error) {
    frag.append(h("div", {
      style: "padding:12px; margin-bottom:16px; background:rgba(239,68,68,0.1); border:1px solid rgba(239,68,68,0.2); border-radius:6px; font-size:0.85rem; font-weight:500; color:#ef4444;"
    }, h("span", {style: "font-weight:700;"}, "Failure Error: "), b.error || "Unknown benchmark failure error. Check console logs for more details."));
  }

  const table = h("table", {class: "data-table", style: "width:100%;"},
    h("thead", {},
      h("tr", {},
        h("th", {}, "Variant/Cell"),
        h("th", {}, "Avg Speed (T/s)"),
        h("th", {}, "Avg Latency (P50)"),
        h("th", {}, "TTFT (P50)"),
        h("th", {}, "Errors")
      )
    ),
    h("tbody", {},
      ...(b.cells || []).map(c => h("tr", {},
        h("td", {}, h("strong", {}, c.label)),
        h("td", {}, h("span", {style: "color:var(--accent-pink);"}, parseFloat(c.aggregates.tg_speed_mean || 0).toFixed(1) + " t/s")),
        h("td", {}, parseFloat(c.aggregates.e2e_p50 || 0).toFixed(0) + "ms"),
        h("td", {}, parseFloat(c.aggregates.ttft_p50 || 0).toFixed(0) + "ms"),
        h("td", {}, parseFloat(c.aggregates.error_rate_percent || 0).toFixed(1) + "%")
      ))
    )
  );
  frag.append(table);
  showModal(`Benchmark Suite Details: ${b.id.substring(6)}`, frag);
};

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
      opt.textContent = p.name;
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
    if (activeLabel) activeLabel.textContent = activeProf.name;

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
        activeLabel.textContent = activeProf ? activeProf.name : "None (Required in payload)";
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
