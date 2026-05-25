/* Llama Server Studio - Main Frontend Application Logic */

document.addEventListener("DOMContentLoaded", () => {
  // Safe DOM builder — use instead of innerHTML for any user/server-supplied data.
  // All string children are appended via createTextNode, so HTML is never parsed.
  // Inline event handlers (on*) are silently dropped; use addEventListener instead.
  // href/src values are checked against an allowlist of safe schemes.
  function h(tag, attrs, ...children) {
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
  const state = {
    models: [],
    profiles: [],
    servers: [],
    benchmarks: [],
    settings: {},
    activeServerId: null,
    logPollInterval: null,
    statsPollInterval: null,
    telemetryHistory: { cpu: [], mem: [] },
  };

  // Auth Queue for requests waiting for token entry
  const authQueue = [];

  // Auth submit handler
  document.getElementById("auth-form").addEventListener("submit", async (e) => {
    e.preventDefault();
    const tokenVal = document.getElementById("auth-token-input").value.trim();
    if (!tokenVal) return;

    localStorage.setItem("admin_token", tokenVal);
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
  const modalTitle = document.getElementById("modal-title");
  const modalContent = document.getElementById("modal-content");
  const modalClose = document.getElementById("btn-close-modal");

  // Initial Boot
  initNavigation();

  // Delegated listener for model-table data-action buttons — eliminates onclick="fn('${id}')" injection
  document.body.addEventListener("click", (e) => {
    const btn = e.target.closest("[data-action]");
    if (!btn) return;
    const id = btn.dataset.modelId;
    if (!id) return;
    switch (btn.dataset.action) {
      case "build": window.createProfileFromModel(id); break;
      case "info":  window.viewModelDetails(id); break;
      case "hide":  window.hideModelFromCatalog(id); break;
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
        
        // Active button
        navButtons.forEach(b => b.classList.remove("active"));
        btn.classList.add("active");

        // Show section
        sections.forEach(sec => sec.classList.remove("active"));
        const activeSec = document.getElementById(`section-${target}`);
        if (activeSec) {
          activeSec.classList.add("active");
        }

        if (target === "dashboard") loadDashboard();
        if (target === "models") loadModelsTable();
        if (target === "profiles") loadProfilesList();
        if (target === "servers") loadServerLifecycleView();
        if (target === "benchmarks") loadBenchmarksHistory();
        if (target === "security") loadSecurityView();
      });
    });

    modalClose.addEventListener("click", () => {
      modal.style.display = "none";
    });
  }

  function showModal(title, content) {
    modalTitle.textContent = title;
    if (content instanceof Node) {
      modalContent.replaceChildren(content);
    } else {
      modalContent.textContent = String(content ?? "");
    }
    modal.style.display = "flex";
  }

  // Helper for human-readable byte sizes
  function formatBytes(bytes) {
    if (bytes === 0) return "0 Bytes";
    const k = 1024;
    const sizes = ["Bytes", "KB", "MB", "GB", "TB"];
    const i = Math.floor(Math.log(bytes) / Math.log(k));
    return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + " " + sizes[i];
  }

  // Helper for readable date strings
  function formatDate(isoStr) {
    if (!isoStr) return "-";
    const date = new Date(isoStr);
    return date.toLocaleString();
  }

  // --- 2. GLOBAL POLLING & API HANDLERS ---

  // ---------------------------------------------------------------------------
  // CORS guide banner
  //
  // fetch() throws TypeError (no HTTP response at all) when the browser blocks
  // a cross-origin request.  The most common cause here is accessing the UI
  // from a non-loopback address (e.g. http://192.168.1.x:3100) while the
  // server's `listen` field is set to `0.0.0.0:3100`.  The server only
  // accepts the exact origin it hears on; every other origin must be listed
  // in `allowed_origins` in config.json.
  //
  // The same TypeError fires when the server is simply offline, so the banner
  // notes that case and stays dismissable.
  // ---------------------------------------------------------------------------

  let _corsGuideSeen = false;

  function showCorsGuide() {
    if (_corsGuideSeen || document.getElementById("cors-guide-banner")) return;
    _corsGuideSeen = true;

    const origin     = window.location.origin;   // e.g. "http://192.168.1.42:3100"
    const configPath = "~/.config/llama-server-studio/config.json";

    // Build the exact JSON patch the user needs to add.
    const snippet =
      `{\n` +
      `  ...\n` +
      `  "allowed_origins": ["${origin}"]\n` +
      `}`;

    // Banner container
    const banner = h("div", {
      id:    "cors-guide-banner",
      role:  "alert",
      style: "position:fixed;top:0;left:0;right:0;z-index:9999;" +
             "background:#7f1d1d;color:#fef2f2;" +
             "padding:16px 56px 16px 20px;font-size:0.85rem;line-height:1.6;" +
             "box-shadow:0 3px 12px rgba(0,0,0,.55);"
    });

    // Title row
    banner.append(h("strong", {style: "font-size:0.95rem;display:block;margin-bottom:6px;"},
      "CORS error — browser blocked a cross-origin request"));

    // Explanation paragraph — built with DOM nodes so no escaping needed
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

    // Config snippet
    const pre = document.createElement("pre");
    pre.style.cssText =
      "background:rgba(0,0,0,.4);padding:10px 14px;border-radius:6px;" +
      "font-size:0.8rem;margin:0 0 10px;overflow-x:auto;white-space:pre;" +
      "border-left:3px solid rgba(255,255,255,.3);";
    pre.textContent = snippet;
    banner.append(pre);

    // Step list
    const steps = document.createElement("ol");
    steps.style.cssText = "margin:0 0 10px;padding-left:1.4em;";
    [
      ["Open ", inlineCode(configPath)],
      ['Add or extend the ', inlineCode('"allowed_origins"'), ' array as shown above.'],
      ["Save the file, then restart llama-server-studio."],
    ].forEach(parts => {
      const li = document.createElement("li");
      li.append(...parts.map(p => typeof p === "string" ? document.createTextNode(p) : p));
      steps.append(li);
    });
    banner.append(steps);

    // Caveat note
    banner.append(h("p", {style: "margin:0;font-size:0.78rem;opacity:.75;"},
      "This banner also appears when the server is not running — dismiss it if that is the case."));

    // Dismiss button
    const closeBtn = h("button", {
      "aria-label": "Dismiss CORS guide",
      style: "position:absolute;top:14px;right:16px;background:transparent;" +
             "border:1px solid rgba(255,255,255,.5);color:inherit;" +
             "padding:4px 12px;border-radius:4px;cursor:pointer;font-size:0.82rem;"
    }, "✕ Dismiss");
    closeBtn.addEventListener("click", () => banner.remove());
    banner.append(closeBtn);

    document.body.prepend(banner);
  }

  async function apiCall(url, method = "GET", body = null) {
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
        
        // Show auth overlay modal
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
        // TypeError can be due to:
        // 1. The server is completely offline/closed (e.g. Go backend stopped or restarting).
        // 2. A genuine CORS block (only possible if page protocol is file:// or a different origin).
        // If the page is loaded over http/https and requests are same-origin (relative),
        // the failure is strictly because the server is offline. Do not show the CORS guide.
        if (window.location.protocol === "file:") {
          showCorsGuide();
        }
      }
      throw err;
    }
  }

  function startGlobalPolling() {
    // Poll running states and stats counters every 4 seconds
    setInterval(() => {
      loadData(false); // silent reload in background
    }, 4000);
  }

  async function loadData(updateUI = true) {
    try {
      state.models = await apiCall("/api/models");
      state.profiles = await apiCall("/api/profiles");
      state.servers = await apiCall("/api/servers");
      
      if (updateUI) {
        loadDashboard();
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
      statusDot.className = "status-dot green";
      statusText.textContent = "Binary Configured";
    } else {
      statusDot.className = "status-dot red";
      statusText.textContent = "Setup Needed";
    }
  }

  // --- 3. DASHBOARD SECTION ---

  function loadDashboard() {
    document.getElementById("dash-models-count").textContent = state.models.filter(m => !m.hidden).length;
    document.getElementById("dash-profiles-count").textContent = state.profiles.length;
    
    const active = state.servers.filter(s => s.status === "healthy" || s.status === "starting");
    const crashed = state.servers.filter(s => s.status === "crashed");
    
    document.getElementById("dash-active-count").textContent = active.length;
    document.getElementById("dash-crashed-count").textContent = crashed.length;
    document.getElementById("active-servers-badge").textContent = `${active.length} Active`;

    // Render running servers
    const serversList = document.getElementById("dash-servers-list");
    if (active.length === 0 && crashed.length === 0) {
      serversList.innerHTML = `<div class="empty-state">No servers currently active. Go to Profile Builder to start one!</div>`;
    } else {
      const allDisplay = [...active, ...crashed];
      serversList.replaceChildren(
        ...allDisplay.map(srv => {
          const prof = state.profiles.find(p => p.id === srv.profile_id);
          const name = prof ? prof.name : "Unknown Profile";
          const statusClass = srv.status === "healthy" ? "green" : (srv.status === "crashed" ? "red" : "yellow");
          const card = h("div", {
            class: "stat-card",
            style: "border: 1px solid rgba(255,255,255,0.05); margin-bottom: 8px; cursor: pointer;"
          },
            h("div", {style: "display:flex; justify-content:space-between; align-items:center;"},
              h("div", {},
                h("strong", {style: "display:block; font-size:0.95rem;"}, name),
                h("span",   {style: "font-size:0.75rem; color:var(--text-dim);"}, `PID: ${srv.pid} | Port: ${srv.port}`)
              ),
              h("span", {class: `status-pill ${statusClass}`}, srv.status)
            )
          );
          card.addEventListener("click", () => {
            document.querySelector("[data-target=servers]").click();
            setTimeout(() => selectServerInLifecycle(srv.id), 100);
          });
          return card;
        })
      );
    }

    // Render recent benchmarks
    loadRecentBenchmarksDashboard();
  }

  async function loadRecentBenchmarksDashboard() {
    const dashBenchList = document.getElementById("dash-bench-list");
    try {
      state.benchmarks = await apiCall("/api/benchmarks");
      const completed = state.benchmarks.filter(b => b.status === "completed").slice(-4).reverse();
      
      if (completed.length === 0) {
        dashBenchList.innerHTML = `<div class="empty-state">No benchmarks completed yet. Go to Server Lifecycle to trigger one!</div>`;
      } else {
        dashBenchList.replaceChildren(
          ...completed.map(b => {
            const prof = state.profiles.find(p => p.id === b.profile_id);
            const name = prof ? prof.name : "Profile";
            const speed = b.result && b.result.avg_tokens_per_sec ? parseFloat(b.result.avg_tokens_per_sec).toFixed(2) : "0";
            return h("div", {style: "padding:12px; border-bottom: 1px solid rgba(255,255,255,0.03); display:flex; justify-content:space-between; align-items:center;"},
              h("div", {},
                h("strong", {style: "display:block; font-size:0.9rem;"}, name),
                h("span",   {style: "font-size:0.7rem; color:var(--text-dim);"}, formatDate(b.started_at))
              ),
              h("span", {style: "font-size:1.1rem; font-weight:800; color:var(--accent-pink);"}, `${speed} T/s`)
            );
          })
        );
      }
    } catch {
      dashBenchList.innerHTML = `<div class="empty-state">Failed to load benchmarks.</div>`;
    }
  }

  // --- 4. MODEL CATALOG SECTION ---

  const searchInput = document.getElementById("model-search");
  const filterSource = document.getElementById("filter-model-source");
  const filterQuant = document.getElementById("filter-model-quant");
  const filterArch = document.getElementById("filter-model-arch");
  const btnRescan = document.getElementById("btn-rescan-models");

  searchInput.addEventListener("input", filterModels);
  filterSource.addEventListener("change", filterModels);
  filterQuant.addEventListener("change", filterModels);
  filterArch.addEventListener("change", filterModels);

  btnRescan.addEventListener("click", async () => {
    const icon = btnRescan.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...btnRescan.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Scanning Disk...";
    btnRescan.disabled = true;
    try {
      await apiCall("/api/models/rescan", "POST");
      await loadData();
      loadModelsTable();
    } catch (err) {
      alert(`Rescan failed: ${err.message}`);
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Rescan Directories";
      btnRescan.disabled = false;
    }
  });

  function populateModelDropdownFilters() {
    const quants = [...new Set(state.models.map(m => m.quantization).filter(Boolean))];
    const archs = [...new Set(state.models.map(m => m.architecture).filter(Boolean))];

    filterQuant.replaceChildren(
      h("option", {value: ""}, "All Quantizations"),
      ...quants.map(q => h("option", {value: q}, q))
    );
    filterArch.replaceChildren(
      h("option", {value: ""}, "All Architectures"),
      ...archs.map(a => h("option", {value: a}, a))
    );
  }

  function loadModelsTable() {
    populateModelDropdownFilters();
    filterModels();
  }

  function filterModels() {
    const query = searchInput.value.toLowerCase();
    const source = filterSource.value;
    const quant = filterQuant.value;
    const arch = filterArch.value;

    const filtered = state.models.filter(m => {
      if (m.hidden) return false;
      const matchesSearch = m.display_name.toLowerCase().includes(query) || (m.repo_id || "").toLowerCase().includes(query) || (m.architecture || "").toLowerCase().includes(query);
      const matchesSource = !source || m.source === source;
      const matchesQuant = !quant || m.quantization === quant;
      const matchesArch = !arch || m.architecture === arch;
      return matchesSearch && matchesSource && matchesQuant && matchesArch;
    });

    const tbody = document.getElementById("models-table-body");
    if (filtered.length === 0) {
      tbody.innerHTML = `<tr><td colspan="8" class="empty-state">No models matching the selected criteria.</td></tr>`;
      return;
    }

    // Helper: create a model-action button with a static SVG icon.
    // Only our own markup goes into innerHTML here — no user/server data.
    function modelBtn(cssClass, action, modelId, svgPath, label) {
      const btn = document.createElement("button");
      btn.className = cssClass;
      btn.dataset.action = action;
      btn.dataset.modelId = modelId;
      btn.innerHTML = `<svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;">${svgPath}</svg>`;
      btn.append(document.createTextNode(" " + label));
      return btn;
    }
    const SVG_BUILD = `<circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"></path>`;
    const SVG_INFO = `<circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line>`;
    const SVG_HIDE = `<path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"></path><line x1="1" y1="1" x2="23" y2="23"></line>`;

    tbody.replaceChildren(
      ...filtered.map(m => {
        const capsCell = document.createElement("td");
        m.capabilities.forEach(c => {
          capsCell.append(h("span", {class: "status-pill purple"}, c), " ");
        });

        const actionsDiv = h("div", {style: "display:flex; gap:8px;"},
          modelBtn("btn btn-sm btn-primary",   "build", m.id, SVG_BUILD, "Build"),
          modelBtn("btn btn-sm btn-secondary", "info",  m.id, SVG_INFO,  "Info"),
          modelBtn("btn btn-sm btn-danger",    "hide",  m.id, SVG_HIDE,  "Hide")
        );

        const nameStrong = h("strong", {style: "color:var(--text-primary); cursor:pointer;",
          "data-action": "info", "data-model-id": m.id}, m.display_name);

        return h("tr", {},
          h("td", {}, nameStrong),
          h("td", {}, h("span", {style: "font-size:0.8rem; color:var(--text-muted);"}, m.source)),
          h("td", {}, h("span", {class: "status-pill yellow"}, m.quantization || "Unknown")),
          h("td", {}, formatBytes(m.size_bytes)),
          h("td", {}, h("code", {style: "color:var(--accent-cyan); font-size:0.8rem;"}, m.architecture || "Unknown")),
          h("td", {}, String(m.context_length || 2048)),
          capsCell,
          h("td", {}, actionsDiv)
        );
      })
    );
  }

  window.viewModelDetails = function(modelID) {
    const m = state.models.find(mod => mod.id === modelID);
    if (!m) return;

    function metaRow(label, ...valueChildren) {
      return h("div", {class: "meta-row"},
        h("span", {class: "lbl"}, label),
        h("span", {class: "val"}, ...valueChildren)
      );
    }

    const frag = document.createDocumentFragment();
    frag.append(
      h("div", {class: "metadata-grid"},
        metaRow("File Name",       m.display_name),
        metaRow("Source",          m.source),
        metaRow("Size",            formatBytes(m.size_bytes)),
        metaRow("Mod Time",        formatDate(m.modified_at)),
        metaRow("Architecture",    m.architecture || "Unknown"),
        metaRow("Quantization",    m.quantization || "Unknown"),
        metaRow("Context Window",  `${m.context_length} tokens`),
        metaRow("Tokenizer model", m.tokenizer_model || "GGUF Native")
      ),
      h("div", {class: "meta-row", style: "margin-top:16px;"},
        h("span", {class: "lbl"}, "Absolute Path"),
        h("code", {class: "val", style: "background:rgba(0,0,0,0.3); padding:8px; border-radius:4px; font-size:0.75rem;"}, m.path)
      )
    );

    if (m.chat_template) {
      const pre = document.createElement("pre");
      pre.style.cssText = "background:rgba(0,0,0,0.5); padding:8px; border-radius:4px; max-height:120px; overflow-y:auto; font-size:0.7rem; color:var(--text-muted);";
      pre.textContent = m.chat_template;   // textContent — no HTML parsing
      frag.append(h("div", {class: "meta-row", style: "margin-top:16px;"},
        h("span", {class: "lbl"}, "Chat Template"),
        pre
      ));
    }

    showModal("Model Details & Header Metadata", frag);
  };

  window.hideModelFromCatalog = async function(modelID) {
    if (confirm("Are you sure you want to hide this GGUF file from the Catalog table?")) {
      try {
        await apiCall(`/api/models/${modelID}/hide`, "POST");
        await loadData();
        loadModelsTable();
      } catch (err) {
        alert(err.message);
      }
    }
  };

  window.createProfileFromModel = function(modelID) {
    document.querySelector("[data-target=profiles]").click();
    setTimeout(() => {
      initProfileEditor();
      document.getElementById("profile-model").value = modelID;
      updateCLIPreview();
    }, 100);
  };

  // Used only in the log viewer, where escaped text is injected as terminal-line HTML.
  // For all other dynamic content use h() or textContent instead.
  function escapeHtml(text) {
    return String(text ?? "").replace(/[&<>"']/g, c => ({
      "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
    }[c]));
  }

  // --- 5. PROFILE BUILDER SECTION ---

  const profileListContainer = document.getElementById("profiles-list-container");
  const profileEditorContainer = document.getElementById("profile-editor-container");
  const newProfileBtn = document.getElementById("btn-create-new-profile");
  const deleteProfileBtn = document.getElementById("btn-delete-profile");
  const profileForm = document.getElementById("profile-form");
  const simplePortPolicy = document.getElementById("simple-port-policy");
  const groupFixedPort = document.getElementById("group-fixed-port");

  // Presets Click Binding
  document.querySelectorAll(".preset-btn").forEach(btn => {
    btn.addEventListener("click", () => {
      const preset = btn.dataset.preset;
      applyPreset(preset);
    });
  });

  function applyPreset(name) {
    const activeModelId = document.getElementById("profile-model").value;
    
    // Clean all inputs first
    document.getElementById("simple-ctx").value = "";
    document.getElementById("simple-ngl").value = "";
    document.getElementById("simple-threads").value = "";
    document.getElementById("simple-batch").value = "";
    document.getElementById("simple-parallel").value = "";
    document.getElementById("simple-routing-enabled").checked = true;
    document.getElementById("simple-routing-autostart").checked = false;
    document.getElementById("simple-routing-policy").value = "latest-ready";
    document.getElementById("simple-ngl").value = "";
    document.getElementById("simple-threads").value = "";
    document.getElementById("simple-batch").value = "";
    document.getElementById("simple-parallel").value = "";
    document.getElementById("adv-gpu-device").value = "";
    document.getElementById("adv-gpu-split").value = "";
    document.getElementById("adv-gpu-tensor").value = "";
    document.getElementById("adv-gpu-main").value = "";
    document.getElementById("adv-mem-flash").value = "auto";
    document.getElementById("adv-mem-mmap").value = "auto";
    document.getElementById("adv-mem-mlock").checked = false;
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-cpu-numa").value = "";
    document.getElementById("adv-lora").value = "";
    document.getElementById("adv-spec-draft").value = "";
    document.getElementById("adv-log-file").value = "";
    document.getElementById("adv-log-verbose").checked = false;
    document.getElementById("adv-diag-perf").checked = true;
    document.getElementById("adv-workdir").value = "";
    document.getElementById("adv-host").value = "";
    document.getElementById("adv-args").value = "";

    if (name === "balanced") {
      document.getElementById("simple-ctx").value = "8192";
      document.getElementById("simple-ngl").value = "auto";
      document.getElementById("adv-mem-flash").value = "auto";
    } else if (name === "cpu") {
      document.getElementById("simple-ngl").value = "0";
      document.getElementById("adv-gpu-device").value = "none";
      document.getElementById("adv-mem-flash").value = "off";
    } else if (name === "gpu-offload") {
      document.getElementById("simple-ngl").value = "all";
      document.getElementById("adv-mem-flash").value = "auto";
    } else if (name === "long-context") {
      document.getElementById("simple-ctx").value = "32768";
      document.getElementById("simple-parallel").value = "1";
    } else if (name === "embedding") {
      document.getElementById("adv-args").value = '["--embedding"]';
    } else if (name === "rerank") {
      document.getElementById("adv-args").value = '["--rerank"]';
    } else if (name === "benchmark") {
      document.getElementById("simple-parallel").value = "1";
      document.getElementById("adv-args").value = '["--temp", "0.0", "--seed", "42"]';
    }

    updateCLIPreview();
  }

  // Tabs
  const profileTabButtons = document.querySelectorAll(".profile-editor .tab-btn");
  const profileTabContents = document.querySelectorAll(".profile-editor .tab-content");

  profileTabButtons.forEach(btn => {
    btn.addEventListener("click", () => {
      profileTabButtons.forEach(b => b.classList.remove("active"));
      btn.classList.add("active");
      profileTabContents.forEach(tc => tc.classList.remove("active"));
      document.getElementById(`profile-tab-${btn.dataset.tab}`).classList.add("active");
    });
  });

  simplePortPolicy.addEventListener("change", () => {
    groupFixedPort.style.display = simplePortPolicy.value === "fixed" ? "block" : "none";
    updateCLIPreview();
  });

  // Watch inputs to update CLI Preview live
  const formInputs = [
    "profile-name", "profile-model",
    "simple-ctx", "simple-ngl", "simple-threads", 
    "simple-batch", "simple-parallel", "simple-port-policy", "simple-fixed-port",
    "adv-args", "adv-workdir", "adv-host",
    "adv-gpu-device", "adv-gpu-split", "adv-gpu-tensor", "adv-gpu-main",
    "adv-mem-flash", "adv-mem-mmap", "adv-mem-mlock", "adv-mem-cacheprompt",
    "adv-cpu-numa", "adv-lora", "adv-spec-draft", "adv-log-file",
    "adv-log-verbose", "adv-diag-perf",
    "simple-routing-enabled", "simple-routing-autostart", "simple-routing-policy"
  ];
  formInputs.forEach(id => {
    const el = document.getElementById(id);
    if (el) {
      el.addEventListener("input", updateCLIPreview);
      el.addEventListener("change", updateCLIPreview);
    }
  });

  // Automatically align context size with the model's native context length
  const profileModelEl = document.getElementById("profile-model");
  if (profileModelEl) {
    profileModelEl.addEventListener("change", () => {
      const modelID = profileModelEl.value;
      if (!modelID) return;
      const model = state.models.find(m => m.id === modelID);
      if (model && model.context_length) {
        const ctxSelect = document.getElementById("simple-ctx");
        const ctxVal = model.context_length;
        
        let exists = false;
        for (let i = 0; i < ctxSelect.options.length; i++) {
          if (parseInt(ctxSelect.options[i].value) === ctxVal) {
            exists = true;
            break;
          }
        }
        
        if (!exists) {
          const opt = document.createElement("option");
          opt.value = ctxVal;
          opt.textContent = `${ctxVal} (Model Native)`;
          ctxSelect.appendChild(opt);
        }
        
        ctxSelect.value = ctxVal;
        updateCLIPreview();
      }
    });
  }

  newProfileBtn.addEventListener("click", () => {
    initProfileEditor();
  });

  function loadProfilesList() {
    profileListContainer.replaceChildren(
      ...state.profiles.map(p => {
        const m = state.models.find(mod => mod.id === p.model_id);
        const modelName = m ? m.display_name : "No Model Selected";
        const btn = h("button", {class: "profile-item-btn", id: `prof-btn-${p.id}`},
          h("strong", {class: "profile-item-title"}, p.name),
          h("span",   {class: "profile-item-meta"},  modelName)
        );
        btn.addEventListener("click", () => selectProfile(p.id));
        return btn;
      })
    );

    if (state.profiles.length > 0) {
      // Auto select first
      selectProfile(state.profiles[0].id);
    } else {
      initProfileEditor();
    }
  }

  function initProfileEditor() {
    profileEditorContainer.style.display = "block";
    document.getElementById("profile-editor-title").textContent = "New Serving Profile";
    document.getElementById("edit-profile-id").value = "";
    profileForm.reset();
    
    // Pre-populate safest and most performant recommended defaults
    document.getElementById("simple-ctx").value = "8192";
    document.getElementById("simple-ngl").value = "auto";
    document.getElementById("simple-threads").value = "-1";
    document.getElementById("simple-parallel").value = "-1";
    
    // Explicitly reset all advanced fields to prevent leaking states
    document.getElementById("adv-gpu-device").value = "";
    document.getElementById("adv-gpu-split").value = "";
    document.getElementById("adv-gpu-tensor").value = "";
    document.getElementById("adv-gpu-main").value = "";
    document.getElementById("adv-mem-flash").value = "auto";
    document.getElementById("adv-mem-mmap").value = "auto";
    document.getElementById("adv-mem-mlock").checked = false;
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-cpu-numa").value = "";
    document.getElementById("adv-lora").value = "";
    document.getElementById("adv-spec-draft").value = "";
    document.getElementById("adv-log-file").value = "";
    document.getElementById("adv-log-verbose").checked = false;
    document.getElementById("adv-diag-perf").checked = true;
    document.getElementById("adv-workdir").value = "";
    document.getElementById("adv-host").value = "127.0.0.1";
    document.getElementById("adv-args").value = '["--no-ui", "-cb", "--metrics", "--slots"]';
    document.getElementById("simple-routing-enabled").checked = true;
    document.getElementById("simple-routing-autostart").checked = false;
    document.getElementById("simple-routing-policy").value = "latest-ready";
    
    // Populate model options
    const modelSelect = document.getElementById("profile-model");
    modelSelect.replaceChildren(
      h("option", {value: ""}, "Select a Model..."),
      ...state.models.filter(m => !m.hidden).map(m => h("option", {value: m.id}, m.display_name))
    );
    
    deleteProfileBtn.style.display = "none";
    groupFixedPort.style.display = "none";
    updateCLIPreview();
  }

  window.selectProfile = function(profileID) {
    const p = state.profiles.find(prof => prof.id === profileID);
    if (!p) return;

    // Set active button style
    document.querySelectorAll(".profile-item-btn").forEach(btn => btn.classList.remove("active"));
    const activeBtn = document.getElementById(`prof-btn-${profileID}`);
    if (activeBtn) activeBtn.classList.add("active");

    profileEditorContainer.style.display = "block";
    document.getElementById("profile-editor-title").textContent = `Edit Profile: ${p.name}`;
    document.getElementById("edit-profile-id").value = p.id;
    document.getElementById("profile-name").value = p.name;
    document.getElementById("profile-desc").value = p.description || "";

    const modelSelect = document.getElementById("profile-model");
    modelSelect.replaceChildren(
      h("option", {value: ""}, "Select a Model..."),
      ...state.models.filter(m => !m.hidden).map(m => h("option", {value: m.id}, m.display_name))
    );
    modelSelect.value = p.model_id;

    // Parse simple settings from args
    let ctx = "", ngl = "", threads = "", batch = "", parallel = "";
    
    const args = p.args;
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-c" && i + 1 < args.length) ctx = parseInt(args[i+1]);
      if (args[i] === "-ngl" && i + 1 < args.length) {
        const val = args[i+1];
        ngl = (val === "auto" || val === "all") ? val : (parseInt(val) || 0);
      }
      if (args[i] === "-t" && i + 1 < args.length) threads = parseInt(args[i+1]);
      if (args[i] === "-b" && i + 1 < args.length) batch = parseInt(args[i+1]);
      if (args[i] === "-np" && i + 1 < args.length) parallel = parseInt(args[i+1]);
    }

    const ctxSelect = document.getElementById("simple-ctx");
    if (ctx !== "") {
      let exists = false;
      for (let i = 0; i < ctxSelect.options.length; i++) {
        if (parseInt(ctxSelect.options[i].value) === ctx) {
          exists = true;
          break;
        }
      }
      if (!exists) {
        const opt = document.createElement("option");
        opt.value = ctx;
        opt.textContent = `${ctx} (Saved)`;
        ctxSelect.appendChild(opt);
      }
    }
    ctxSelect.value = ctx;
    document.getElementById("simple-ngl").value = ngl;
    document.getElementById("simple-threads").value = threads;
    document.getElementById("simple-batch").value = batch;
    document.getElementById("simple-parallel").value = parallel;

    simplePortPolicy.value = p.default_port_policy;
    if (p.default_port_policy === "fixed") {
      groupFixedPort.style.display = "block";
      document.getElementById("simple-fixed-port").value = p.fixed_port;
    } else {
      groupFixedPort.style.display = "none";
    }

    // Advanced fields
    let gpuDevice = "", gpuSplit = "", gpuTensor = "", gpuMain = "";
    let memFlash = "auto", memMmap = "auto", memMlock = false, memCachePrompt = true;
    let cpuNuma = "";
    let lora = "", specDraft = "", logFile = "";
    let logVerbose = false, diagPerf = true;

    const parsedFlags = [
      "-c", "-ngl", "-t", "-b", "-np", "-m", "--model", "--host", "--port", "-p",
      "--device", "-sm", "-ts", "-mg", "--flash-attn", "--no-flash-attn", 
      "--mmap", "--no-mmap", "--mlock", "--no-cache-prompt", "--numa",
      "--lora", "--model-draft", "--log-file", "--verbose", "-v", "--log-verbose",
      "--perf", "--no-perf"
    ];

    const flagsWithArgs = [
      "-c", "-ngl", "-t", "-b", "-np", "-m", "--model", "--host", "--port", "-p",
      "--device", "-sm", "-ts", "-mg", "--numa", "--lora", "--model-draft", "--log-file"
    ];

    const advArr = [];
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "--device" && i + 1 < args.length) { gpuDevice = args[i+1]; i++; }
      else if (args[i] === "-sm" && i + 1 < args.length) { gpuSplit = args[i+1]; i++; }
      else if (args[i] === "-ts" && i + 1 < args.length) { gpuTensor = args[i+1]; i++; }
      else if (args[i] === "-mg" && i + 1 < args.length) { gpuMain = args[i+1]; i++; }
      else if (args[i] === "--flash-attn") { memFlash = "on"; }
      else if (args[i] === "--no-flash-attn") { memFlash = "off"; }
      else if (args[i] === "--mmap") { memMmap = "on"; }
      else if (args[i] === "--no-mmap") { memMmap = "off"; }
      else if (args[i] === "--mlock") { memMlock = true; }
      else if (args[i] === "--no-cache-prompt") { memCachePrompt = false; }
      else if (args[i] === "--numa" && i + 1 < args.length) { cpuNuma = args[i+1]; i++; }
      else if (args[i] === "--lora" && i + 1 < args.length) { lora = args[i+1]; i++; }
      else if (args[i] === "--model-draft" && i + 1 < args.length) { specDraft = args[i+1]; i++; }
      else if (args[i] === "--log-file" && i + 1 < args.length) { logFile = args[i+1]; i++; }
      else if (args[i] === "--verbose" || args[i] === "-v" || args[i] === "--log-verbose") { logVerbose = true; }
      else if (args[i] === "--perf") { diagPerf = true; }
      else if (args[i] === "--no-perf") { diagPerf = false; }
      else if (parsedFlags.includes(args[i])) {
        // Skip structured parameters
        if (flagsWithArgs.includes(args[i])) {
          i++; // skip next element
        }
      } else {
        advArr.push(args[i]);
      }
    }

    document.getElementById("adv-gpu-device").value = gpuDevice;
    document.getElementById("adv-gpu-split").value = gpuSplit;
    document.getElementById("adv-gpu-tensor").value = gpuTensor;
    if (gpuMain) {
      document.getElementById("adv-gpu-main").value = gpuMain;
    } else {
      document.getElementById("adv-gpu-main").value = "";
    }
    document.getElementById("adv-mem-flash").value = memFlash;
    document.getElementById("adv-mem-mmap").value = memMmap;
    document.getElementById("adv-mem-mlock").checked = memMlock;
    document.getElementById("adv-mem-cacheprompt").checked = memCachePrompt;
    document.getElementById("adv-cpu-numa").value = cpuNuma;
    document.getElementById("adv-lora").value = lora;
    document.getElementById("adv-spec-draft").value = specDraft;
    document.getElementById("adv-log-file").value = logFile;
    document.getElementById("adv-log-verbose").checked = logVerbose;
    document.getElementById("adv-diag-perf").checked = diagPerf;

    document.getElementById("adv-args").value = advArr.length > 0 ? JSON.stringify(advArr) : "";
    document.getElementById("adv-workdir").value = p.working_dir || "";
    document.getElementById("adv-host").value = p.default_host || "127.0.0.1";

    const routing = p.routing || { enabled: true, autoStart: false, primaryInstancePolicy: "latest-ready" };
    document.getElementById("simple-routing-enabled").checked = routing.enabled !== false;
    document.getElementById("simple-routing-autostart").checked = routing.autoStart === true;
    document.getElementById("simple-routing-policy").value = routing.primaryInstancePolicy || "latest-ready";

    deleteProfileBtn.style.display = "inline-flex";
    updateCLIPreview();
  };

  // Live CLI Preview Generator
  function generateCommandArgs() {
    const modelID = document.getElementById("profile-model").value;
    const m = state.models.find(mod => mod.id === modelID);
    const modelPath = m ? m.resolved_path : "[model-file-path]";

    const ctx = document.getElementById("simple-ctx").value;
    const ngl = document.getElementById("simple-ngl").value.trim();
    const threads = document.getElementById("simple-threads").value.trim();
    const batch = document.getElementById("simple-batch").value.trim();
    const parallel = document.getElementById("simple-parallel").value.trim();
    const portPolicy = simplePortPolicy.value;
    const fixedPort = document.getElementById("simple-fixed-port").value;
    const host = document.getElementById("adv-host").value.trim();

    const builtArgs = [
      "-m", modelPath
    ];

    if (ctx) builtArgs.push("-c", ctx);
    if (ngl !== "") builtArgs.push("-ngl", ngl);
    if (threads !== "") builtArgs.push("-t", threads);
    if (batch !== "") builtArgs.push("-b", batch);
    if (parallel !== "") builtArgs.push("-np", parallel);
    if (host) builtArgs.push("--host", host);

    if (portPolicy === "fixed") {
      builtArgs.push("--port", fixedPort);
    } else {
      builtArgs.push("--port", "[allocated-port]");
    }

    // Append structured advanced arguments
    const gpuDevice = document.getElementById("adv-gpu-device").value.trim();
    if (gpuDevice) builtArgs.push("--device", gpuDevice);

    const gpuSplit = document.getElementById("adv-gpu-split").value;
    if (gpuSplit) builtArgs.push("-sm", gpuSplit);

    const gpuTensor = document.getElementById("adv-gpu-tensor").value.trim();
    if (gpuTensor) builtArgs.push("-ts", gpuTensor);

    const gpuMain = document.getElementById("adv-gpu-main").value.trim();
    if (gpuMain) builtArgs.push("-mg", gpuMain);

    const memFlash = document.getElementById("adv-mem-flash").value;
    if (memFlash === "on") builtArgs.push("--flash-attn");
    else if (memFlash === "off") builtArgs.push("--no-flash-attn");

    const memMmap = document.getElementById("adv-mem-mmap").value;
    if (memMmap === "on") builtArgs.push("--mmap");
    else if (memMmap === "off") builtArgs.push("--no-mmap");

    if (document.getElementById("adv-mem-mlock").checked) {
      builtArgs.push("--mlock");
    }

    if (!document.getElementById("adv-mem-cacheprompt").checked) {
      builtArgs.push("--no-cache-prompt");
    }

    const cpuNuma = document.getElementById("adv-cpu-numa").value;
    if (cpuNuma) builtArgs.push("--numa", cpuNuma);

    const lora = document.getElementById("adv-lora").value.trim();
    if (lora) builtArgs.push("--lora", lora);

    const specDraft = document.getElementById("adv-spec-draft").value.trim();
    if (specDraft) builtArgs.push("--model-draft", specDraft);

    const logFile = document.getElementById("adv-log-file").value.trim();
    if (logFile) builtArgs.push("--log-file", logFile);

    if (document.getElementById("adv-log-verbose").checked) {
      builtArgs.push("--verbose");
    }

    if (!document.getElementById("adv-diag-perf").checked) {
      builtArgs.push("--no-perf");
    }

    // Append advanced raw flags if present
    const advStr = document.getElementById("adv-args").value.trim();
    if (advStr) {
      try {
        const parsed = JSON.parse(advStr);
        if (Array.isArray(parsed)) {
          builtArgs.push(...parsed);
        }
      } catch {}
    }

    return builtArgs;
  }

  function updateCLIPreview() {
    const args = generateCommandArgs();
    const executable = "llama-server";
    
    // Highlight elements
    const render = `${executable} \\\n` + args.map((arg, idx) => {
      let val = arg;
      if (arg.includes(" ") || arg.includes("\\")) {
        val = `"${arg}"`;
      }
      return `  ${val}` + (idx === args.length - 1 ? "" : " \\");
    }).join("\n");

    document.getElementById("cli-command-text").textContent = render;
    
    // Perform dynamic validation checks
    validateActiveProfile();
  }

  function validateActiveProfile() {
    const name = document.getElementById("profile-name").value.trim();
    const modelID = document.getElementById("profile-model").value;
    const ngl = document.getElementById("simple-ngl").value.trim();
    const threads = document.getElementById("simple-threads").value.trim();
    const ctx = document.getElementById("simple-ctx").value;
    const portPolicy = simplePortPolicy.value;
    const fixedPort = document.getElementById("simple-fixed-port").value.trim();
    const parallel = document.getElementById("simple-parallel").value.trim();
    const host = document.getElementById("adv-host").value.trim();
    const advStr = document.getElementById("adv-args").value.trim();

    const diagnostics = [];

    // 1. Errors
    if (!name) {
      diagnostics.push({ severity: "error", message: "Profile name is required." });
    }
    if (!modelID) {
      diagnostics.push({ severity: "error", message: "Model selection is required. Please select a local GGUF model." });
    }
    if (portPolicy === "fixed") {
      const portNum = parseInt(fixedPort);
      if (isNaN(portNum) || portNum < 1 || portNum > 65535) {
        diagnostics.push({ severity: "error", message: "Fixed port number must be a valid port between 1 and 65535." });
      }
    }
    if (threads !== "") {
      const threadsNum = parseInt(threads);
      if (isNaN(threadsNum) || (threadsNum <= 0 && threadsNum !== -1)) {
        diagnostics.push({ severity: "error", message: "CPU Threads must be a positive integer or -1 (for auto)." });
      }
    }
    if (advStr) {
      try {
        const parsed = JSON.parse(advStr);
        if (!Array.isArray(parsed)) {
          diagnostics.push({ severity: "error", message: "Custom CLI flags must be formatted as a valid JSON array of strings." });
        }
      } catch {
        diagnostics.push({ severity: "error", message: "Custom CLI flags textbox contains malformed JSON. Example format: [\"--embedding\", \"-cb\"]" });
      }
    }

    // 2. Warnings
    if (ngl !== "" && ngl !== "auto" && ngl !== "all") {
      const nglNum = parseInt(ngl);
      if (!isNaN(nglNum) && nglNum > 120) {
        diagnostics.push({ severity: "warning", message: "Offloading more than 120 layers may exceed your model size and exhaust VRAM." });
      }
    }
    if (threads !== "") {
      const threadsNum = parseInt(threads);
      if (!isNaN(threadsNum) && threadsNum > 16) {
        diagnostics.push({ severity: "warning", message: "Allocating more than 16 CPU threads might hurt performance due to core contention." });
      }
    }
    if (ctx && parseInt(ctx) > 32768) {
      diagnostics.push({ severity: "warning", message: "Context sizes above 32k tokens dramatically increase memory footprints." });
    }
    if (host === "0.0.0.0") {
      diagnostics.push({ severity: "warning", message: "Exposing the server on 0.0.0.0 makes it accessible to your entire local network. Ensure firewalls are configured." });
    }

    // 3. Info
    if (ngl === "" || ngl === "auto") {
      diagnostics.push({ severity: "info", message: "GPU Layers set to Auto: llama-server will detect and allocate layers." });
    }
    if (!ctx) {
      diagnostics.push({ severity: "info", message: "Context size set to Default: context window will adapt to GGUF model metadata." });
    }
    if (parallel === "" || parallel === "1") {
      diagnostics.push({ severity: "info", message: "Isolated parallel slots: single session active, minimizing VRAM cache." });
    }

    // Render Diagnostics Console
    const consoleBox = document.getElementById("validation-console");
    const container = document.getElementById("validation-errors");
    
    if (diagnostics.length === 0) {
      consoleBox.style.display = "none";
      return true; // Validated
    }

    consoleBox.style.display = "block";
    container.innerHTML = diagnostics.map(d => {
      let icon = "";
      let color = "";
      if (d.severity === "error") {
        icon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="width:12px; height:12px; margin-right:6px; vertical-align:middle; display:inline-block;"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="8" x2="12" y2="12"></line><line x1="12" y1="16" x2="12.01" y2="16"></line></svg>`;
        color = "color:var(--accent-red); font-weight:600;";
      } else if (d.severity === "warning") {
        icon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="width:12px; height:12px; margin-right:6px; vertical-align:middle; display:inline-block;"><path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path><line x1="12" y1="9" x2="12" y2="13"></line><line x1="12" y1="17" x2="12.01" y2="17"></line></svg>`;
        color = "color:var(--accent-yellow);";
      } else {
        icon = `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" style="width:12px; height:12px; margin-right:6px; vertical-align:middle; display:inline-block;"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>`;
        color = "color:var(--text-dim); font-style:italic;";
      }
      return `
        <div style="font-size:0.8rem; display:flex; align-items:flex-start; ${color} line-height:1.4;">
          <span style="display:inline-flex; align-items:center; padding-top:2px; flex-shrink:0;">${icon}</span>
          <span>${d.message}</span>
        </div>
      `;
    }).join("");

    const hasErrors = diagnostics.some(d => d.severity === "error");
    const saveBtn = document.getElementById("btn-save-profile");
    const runBtn = document.getElementById("btn-save-run-profile");
    
    if (hasErrors) {
      saveBtn.disabled = true;
      runBtn.disabled = true;
      saveBtn.style.opacity = "0.5";
      runBtn.style.opacity = "0.5";
      saveBtn.style.cursor = "not-allowed";
      runBtn.style.cursor = "not-allowed";
      return false; // validation failed
    } else {
      saveBtn.disabled = false;
      runBtn.disabled = false;
      saveBtn.style.opacity = "1";
      runBtn.style.opacity = "1";
      saveBtn.style.cursor = "pointer";
      runBtn.style.cursor = "pointer";
      return true; // validation passed (only warnings or info)
    }
  }

  // Copy command button
  document.getElementById("btn-copy-cli-command").addEventListener("click", () => {
    const txt = document.getElementById("cli-command-text").textContent;
    navigator.clipboard.writeText(txt);
    alert("Invocations copied to clipboard!");
  });

  // Profile Save
  profileForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    await saveActiveProfile();
  });

  // Save & Run helper
  document.getElementById("btn-save-run-profile").addEventListener("click", async () => {
    const p = await saveActiveProfile();
    if (p) {
      await launchServerInstance(p.id);
    }
  });

  async function saveActiveProfile() {
    // Prevent saves if validation yields errors
    if (!validateActiveProfile()) {
      alert("Validation errors detected. Please resolve all red error diagnostics before saving this profile.");
      return null;
    }

    const profileID = document.getElementById("edit-profile-id").value;
    const name = document.getElementById("profile-name").value;
    const modelID = document.getElementById("profile-model").value;
    const desc = document.getElementById("profile-desc").value;

    if (!modelID) {
      alert("Please select a valid model.");
      return null;
    }

    // Assemble profile parameters back to arguments list
    const args = generateCommandArgs();
    
    // Filter out mock strings
    const cleanArgs = args.filter(a => a !== "[model-file-path]" && a !== "[allocated-port]");

    const payload = {
      name,
      description: desc,
      model_id: modelID,
      args: cleanArgs,
      default_host: document.getElementById("adv-host").value || "127.0.0.1",
      default_port_policy: simplePortPolicy.value,
      fixed_port: parseInt(document.getElementById("simple-fixed-port").value) || 8080,
      working_dir: document.getElementById("adv-workdir").value || "",
      routing: {
        enabled: document.getElementById("simple-routing-enabled").checked,
        autoStart: document.getElementById("simple-routing-autostart").checked,
        primaryInstancePolicy: document.getElementById("simple-routing-policy").value,
        publicPath: `/profiles/${profileID || 'new'}/v1`
      }
    };

    try {
      let result;
      if (profileID) {
        // Edit
        result = await apiCall(`/api/profiles/${profileID}`, "PUT", payload);
      } else {
        // Create
        result = await apiCall("/api/profiles", "POST", payload);
      }
      await loadData();
      loadProfilesList();
      alert("Serving Profile saved successfully!");
      return result;
    } catch (err) {
      alert(`Save failed: ${err.message}`);
      return null;
    }
  }

  // Delete profile
  deleteProfileBtn.addEventListener("click", async () => {
    const id = document.getElementById("edit-profile-id").value;
    if (confirm("Are you sure you want to delete this profile?")) {
      try {
        await apiCall(`/api/profiles/${id}`, "DELETE");
        await loadData();
        loadProfilesList();
      } catch (err) {
        alert(err.message);
      }
    }
  });

  // Profile Exports JSON/Shell
  document.getElementById("btn-export-profile-json").addEventListener("click", () => {
    const id = document.getElementById("edit-profile-id").value;
    if (!id) return;
    window.open(`/api/profiles/${id}/export.json`);
  });

  document.getElementById("btn-export-profile-sh").addEventListener("click", () => {
    const id = document.getElementById("edit-profile-id").value;
    if (!id) return;
    window.open(`/api/profiles/${id}/export.sh`);
  });

  // --- 6. SERVER LIFECYCLE SECTION ---

  const lifecycleProfilesList = document.getElementById("lifecycle-profiles-list");
  const lifecycleDetails = document.getElementById("lifecycle-details-container");
  const lifecycleEmpty = document.getElementById("lifecycle-empty-state");

  const btnStartSrv = document.getElementById("btn-start-srv");
  const btnStopSrv = document.getElementById("btn-stop-srv");
  const btnRestartSrv = document.getElementById("btn-restart-srv");

  // Helper to start server from ANY tab
  async function launchServerInstance(profileID) {
    try {
      const srvRecord = await apiCall(`/api/profiles/${profileID}/start`, "POST");
      await loadData();
      
      // Select the profile in lifecycle tab
      state.activeProfileIdInLifecycle = profileID;
      document.querySelector("[data-target=servers]").click();
      setTimeout(() => {
        selectProfileInLifecycle(profileID);
      }, 200);
    } catch (err) {
      alert(`Start failed: ${err.message}`);
    }
  }

  // Populate profiles in the left lifecycle list
  function loadServerLifecycleView() {
    if (!lifecycleProfilesList) return;
    if (state.profiles.length === 0) {
      lifecycleProfilesList.replaceChildren(h("div", {class: "empty-state"}, "No saved profiles."));
      lifecycleDetails.style.display = "none";
      lifecycleEmpty.style.display = "block";
      clearIntervals();
      return;
    }

    lifecycleProfilesList.replaceChildren(
      ...state.profiles.map(p => {
        const activeSrv = state.servers.find(s => s.profile_id === p.id && s.status !== "stopped");
        const status = activeSrv ? activeSrv.status : "stopped";
        
        let statusCls = "gray";
        if (status === "healthy" || status === "ready") statusCls = "green";
        else if (status === "crashed") statusCls = "red";
        else if (status === "starting" || status === "loading") statusCls = "yellow";

        const m = state.models.find(mod => mod.id === p.model_id);
        const modelName = m ? m.display_name : "GGUF Model";

        const btn = h("button", {
          class: `profile-item-btn ${state.activeProfileIdInLifecycle === p.id ? "active" : ""}`,
          style: "display: flex; flex-direction: column; width: 100%; text-align: left;"
        },
          h("div", {style: "display:flex; justify-content:space-between; align-items:center; width:100%;"},
            h("strong", {class: "profile-item-title"}, p.name),
            h("span", {class: `status-pill ${statusCls}`, style: "font-size:0.65rem; padding: 2px 6px;"}, status)
          ),
          h("span", {class: "profile-item-meta"}, modelName)
        );

        btn.addEventListener("click", () => selectProfileInLifecycle(p.id));
        return btn;
      })
    );

    // Auto-select active or first profile on first load
    if (state.activeProfileIdInLifecycle) {
      selectProfileInLifecycle(state.activeProfileIdInLifecycle);
    } else if (state.profiles.length > 0) {
      selectProfileInLifecycle(state.profiles[0].id);
    }
  }

  // Hook up select server by profile ID
  window.selectProfileInLifecycle = function(profileID) {
    state.activeProfileIdInLifecycle = profileID;
    
    // Highlight sidebar active item
    if (lifecycleProfilesList) {
      const buttons = lifecycleProfilesList.querySelectorAll(".profile-item-btn");
      buttons.forEach((btn, idx) => {
        const p = state.profiles[idx];
        if (p && p.id === profileID) {
          btn.classList.add("active");
        } else {
          btn.classList.remove("active");
        }
      });
    }

    const p = state.profiles.find(prof => prof.id === profileID);
    if (!p) {
      lifecycleDetails.style.display = "none";
      lifecycleEmpty.style.display = "block";
      clearIntervals();
      return;
    }

    lifecycleDetails.style.display = "block";
    lifecycleEmpty.style.display = "none";

    const s = state.servers.find(srv => srv.profile_id === profileID && srv.status !== "stopped");
    document.getElementById("stable-route-url").textContent = `/profiles/${profileID}/v1/completions`;

    if (!s) {
      // Server is stopped
      clearIntervals();
      state.activeServerId = null;

      // Show start button, hide stop/restart
      btnStartSrv.style.display = "inline-flex";
      btnStopSrv.style.display = "none";
      btnRestartSrv.style.display = "none";

      document.getElementById("srv-status-badge").className = "tel-val status-pill gray";
      document.getElementById("srv-status-badge").textContent = "stopped";
      document.getElementById("srv-pid-val").textContent = "-";
      document.getElementById("srv-port-val").textContent = p.port || "-";
      document.getElementById("srv-uptime-val").textContent = "Stopped";

      document.getElementById("realtime-cpu").textContent = "0%";
      document.getElementById("realtime-mem").textContent = "0.00 GB";

      // Console placeholder
      document.getElementById("server-log-console").replaceChildren(
        h("div", {class: "terminal-line system-line"}, `[System] Server for profile "${p.name}" is currently offline.`),
        h("div", {class: "terminal-line system-line"}, `[System] Click "Start Server" to boot it using GGUF model: ${p.model_id}`)
      );
    } else {
      // Server is running/starting/crashed
      state.activeServerId = s.id;
      state.lastLogCount = 0;
      const consoleBox = document.getElementById("server-log-console");
      if (consoleBox) {
        consoleBox.replaceChildren(h("div", {class: "terminal-line system-line"}, "[System] Connecting to console stream..."));
      }

      // Hide start button, show stop/restart
      btnStartSrv.style.display = "none";
      btnStopSrv.style.display = "inline-flex";
      btnRestartSrv.style.display = "inline-flex";

      const statusCls = s.status === "healthy" || s.status === "ready" ? "green" : (s.status === "crashed" ? "red" : "yellow");
      document.getElementById("srv-status-badge").className = `tel-val status-pill ${statusCls}`;
      document.getElementById("srv-status-badge").textContent = s.status;
      document.getElementById("srv-pid-val").textContent = s.pid || "-";
      document.getElementById("srv-port-val").textContent = s.port;

      // Start polling
      clearIntervals();
      pollServerLogs(s.id);
      pollServerTelemetry(s.id);
      state.logPollInterval = setInterval(() => pollServerLogs(s.id), 1000);
      state.statsPollInterval = setInterval(() => pollServerTelemetry(s.id), 1500);
    }
  };

  // Keep compatibility for clicking on servers from dashboard
  window.selectServerInLifecycle = function(serverID) {
    const s = state.servers.find(srv => srv.id === serverID);
    if (s) {
      selectProfileInLifecycle(s.profile_id);
    }
  };

  function clearIntervals() {
    if (state.logPollInterval) clearInterval(state.logPollInterval);
    if (state.statsPollInterval) clearInterval(state.statsPollInterval);
  }

  async function pollServerLogs(serverID) {
    try {
      const logs = await apiCall(`/api/servers/${serverID}/logs`);
      const consoleBox = document.getElementById("server-log-console");
      if (!consoleBox) return;

      if (logs.length === 0) {
        state.lastLogCount = 0;
        consoleBox.replaceChildren(h("div", {class: "terminal-line system-line"}, "[System] Log empty. Server starting..."));
        return;
      }

      // If logs shrank or changed (e.g. server restarted), clear and reset
      if (logs.length < state.lastLogCount) {
        state.lastLogCount = 0;
        consoleBox.replaceChildren();
      }

      // Only perform work if there are new lines to display
      if (logs.length > state.lastLogCount) {
        // Detect if user is scrolled near the bottom (within 40px) to lock scrolling
        const isAtBottom = consoleBox.scrollHeight - consoleBox.scrollTop - consoleBox.clientHeight < 40;

        // Slice only the new logs to append
        const newLines = logs.slice(state.lastLogCount);
        const fragment = document.createDocumentFragment();

        newLines.forEach(line => {
          let c = "terminal-line";
          if (line.includes("[System]") || line.includes("LLAMA SERVER STUDIO")) c = "terminal-line system-line";
          if (line.includes("error") || line.includes("fail") || line.includes("ERR")) c = "terminal-line err-line";
          const div = document.createElement("div");
          div.className = c;
          div.textContent = line;
          fragment.appendChild(div);
        });

        // If it was the very first load or logs were just cleared, replace placeholder completely
        if (state.lastLogCount === 0) {
          consoleBox.replaceChildren(fragment);
        } else {
          consoleBox.appendChild(fragment);
        }

        state.lastLogCount = logs.length;

        // Scroll to bottom only if they were already at the bottom (prevents hijacking user scroll)
        if (isAtBottom || state.lastLogCount === newLines.length) {
          consoleBox.scrollTop = consoleBox.scrollHeight;
        }
      }
    } catch {
      document.getElementById("server-log-console").replaceChildren(
        h("div", {class: "terminal-line err-line"}, "[System] Failed to read disk logs.")
      );
    }
  }

  async function pollServerTelemetry(serverID) {
    try {
      // Reload server config silently to refresh status
      state.servers = await apiCall("/api/servers");
      const s = state.servers.find(srv => srv.id === serverID);
      if (s) {
        const statusCls = s.status === "healthy" || s.status === "ready" ? "green" : (s.status === "crashed" ? "red" : "yellow");
        document.getElementById("srv-status-badge").className = `tel-val status-pill ${statusCls}`;
        document.getElementById("srv-status-badge").textContent = s.status;
        document.getElementById("srv-pid-val").textContent = s.pid || "-";
        
        // Calculate uptime
        if (s.started_at && s.pid > 0) {
          const elapsed = Math.floor((new Date() - new Date(s.started_at)) / 1000);
          const mins = Math.floor(elapsed / 60).toString().padStart(2, '0');
          const secs = (elapsed % 60).toString().padStart(2, '0');
          document.getElementById("srv-uptime-val").textContent = `${mins}:${secs}`;
        } else {
          document.getElementById("srv-uptime-val").textContent = "Stopped";
        }
      }

      const samples = await apiCall(`/api/servers/${serverID}/stats`);
      if (samples.length > 0) {
        const last = samples[samples.length - 1];
        document.getElementById("realtime-cpu").textContent = `${parseFloat(last.cpu_percent).toFixed(1)}%`;
        document.getElementById("realtime-mem").textContent = `${(last.memory_rss_bytes / 1024 / 1024 / 1024).toFixed(2)} GB`;

        // Update charts history
        state.telemetryHistory.cpu = samples.map(sa => sa.cpu_percent);
        state.telemetryHistory.mem = samples.map(sa => sa.memory_rss_bytes / 1024 / 1024 / 1024); // GB
        drawTelemetryCanvas();
      }
    } catch {}
  }

  // Start click (Lifecycle)
  btnStartSrv.addEventListener("click", async () => {
    const profileID = state.activeProfileIdInLifecycle;
    if (!profileID) return;
    
    const icon = btnStartSrv.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...btnStartSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Starting...";
    btnStartSrv.disabled = true;
    
    try {
      await apiCall(`/api/profiles/${profileID}/start`, "POST");
      await loadData();
      selectProfileInLifecycle(profileID);
    } catch (err) {
      alert(`Start failed: ${err.message}`);
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Start Server";
      btnStartSrv.disabled = false;
    }
  });

  // Stop click
  btnStopSrv.addEventListener("click", async () => {
    if (state.activeServerId) {
      const icon = btnStopSrv.querySelector(".btn-icon-svg");
      if (icon) icon.classList.add("spin");
      const textNode = [...btnStopSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
      if (textNode) textNode.textContent = " Stopping...";
      btnStopSrv.disabled = true;
      try {
        await apiCall(`/api/servers/${state.activeServerId}/stop`, "POST");
        await loadData();
        selectProfileInLifecycle(state.activeProfileIdInLifecycle);
      } catch (err) {
        alert(err.message);
      } finally {
        if (icon) icon.classList.remove("spin");
        if (textNode) textNode.textContent = " Stop Server";
        btnStopSrv.disabled = false;
      }
    }
  });

  // Restart click
  btnRestartSrv.addEventListener("click", async () => {
    if (state.activeServerId) {
      const icon = btnRestartSrv.querySelector(".btn-icon-svg");
      if (icon) icon.classList.add("spin");
      const textNode = [...btnRestartSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
      if (textNode) textNode.textContent = " Restarting...";
      btnRestartSrv.disabled = true;
      try {
        await apiCall(`/api/servers/${state.activeServerId}/restart`, "POST");
        await loadData();
        selectProfileInLifecycle(state.activeProfileIdInLifecycle);
      } catch (err) {
        alert(err.message);
      } finally {
        if (icon) icon.classList.remove("spin");
        if (textNode) textNode.textContent = " Restart Server";
        btnRestartSrv.disabled = false;
      }
    }
  });

  // Download Logs click
  document.getElementById("btn-download-logs").addEventListener("click", () => {
    if (state.activeServerId) {
      window.open(`/api/servers/${state.activeServerId}/logs/download`);
    }
  });

  document.getElementById("btn-clear-terminal").addEventListener("click", () => {
    document.getElementById("server-log-console").innerHTML = `<div class="terminal-line system-line">[System] Cleared output console. Logs will stream on next tick.</div>`;
  });

  // Canvas Sparks Graph drawing
  function drawTelemetryCanvas() {
    const canvas = document.getElementById("chart-canvas");
    if (!canvas) return;
    const ctx = canvas.getContext("2d");
    const width = canvas.width;
    const height = canvas.height;

    ctx.clearRect(0, 0, width, height);

    const cpuData = state.telemetryHistory.cpu;
    if (cpuData.length < 2) return;

    // Dynamically retrieve active CSS custom properties for proper paper-white theme coordination
    const computedStyle = getComputedStyle(document.documentElement);
    const borderSoft = computedStyle.getPropertyValue("--border-soft").trim() || "#eee6d9";
    const accentPink = computedStyle.getPropertyValue("--accent-pink").trim() || "#8b5e34";

    // Draw grid
    ctx.strokeStyle = borderSoft;
    ctx.lineWidth = 1;
    for (let i = 20; i < width; i += 20) {
      ctx.beginPath();
      ctx.moveTo(i, 0);
      ctx.lineTo(i, height);
      ctx.stroke();
    }
    for (let i = 20; i < height; i += 20) {
      ctx.beginPath();
      ctx.moveTo(0, i);
      ctx.lineTo(width, i);
      ctx.stroke();
    }

    // Plot CPU sparkline
    ctx.beginPath();
    ctx.strokeStyle = accentPink;
    ctx.lineWidth = 2.5;
    
    const step = width / (cpuData.length - 1);
    for (let idx = 0; idx < cpuData.length; idx++) {
      const val = cpuData[idx]; // 0 - 100%
      const x = idx * step;
      const y = height - ((val / 100) * (height - 10)) - 5;
      
      if (idx === 0) {
        ctx.moveTo(x, y);
      } else {
        ctx.lineTo(x, y);
      }
    }
    ctx.stroke();
  }

  // --- 7. QUICK PROMPT TEST PLAYGROUND ---

  const testBtn = document.getElementById("btn-submit-test");
  const testOutputBox = document.getElementById("test-response-output");
  const testPromptText = document.getElementById("test-prompt");

  testBtn.addEventListener("click", async () => {
    if (!state.activeServerId) return;

    const s = state.servers.find(srv => srv.id === state.activeServerId);
    if (!s || s.status !== "healthy") {
      alert("llama-server must be fully running and healthy to send prompts.");
      return;
    }

    const prompt = testPromptText.value.trim();
    if (!prompt) return;

    const temp = parseFloat(document.getElementById("test-temp").value) || 0.7;
    const tokens = parseInt(document.getElementById("test-tokens").value) || 128;
    const stream = document.getElementById("test-stream").checked;

    const icon = testBtn.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...testBtn.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Querying...";
    testBtn.disabled = true;
    testOutputBox.innerHTML = `<span class="placeholder-text">Executing request...</span>`;

    const url = `/api/servers/${state.activeServerId}/test`;
    const payload = { prompt, temp, max_tokens: tokens, stream };

    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload)
      });

      if (!response.ok) {
        throw new Error(`Inference returned HTTP ${response.status}`);
      }

      if (stream) {
        testOutputBox.innerHTML = ""; // Clear placeholder
        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = "";

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;

          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split("\n");
          buffer = lines.pop(); // Retain leftover

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

  // Copy curl code block
  document.getElementById("btn-copy-curl").addEventListener("click", () => {
    if (!state.activeServerId) return;
    const s = state.servers.find(srv => srv.id === state.activeServerId);
    if (!s) return;

    const host = s.host === "0.0.0.0" ? "127.0.0.1" : s.host;
    const prompt = testPromptText.value.trim() || "Hello local llama!";
    const temp = parseFloat(document.getElementById("test-temp").value) || 0.7;
    const tokens = parseInt(document.getElementById("test-tokens").value) || 128;

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

  // --- 8. BENCHMARKS HISTORY SECTION ---

  const btnTriggerBench = document.getElementById("btn-trigger-bench");

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

  async function loadBenchmarksHistory() {
    const tbody = document.getElementById("benchmarks-table-body");
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
          // Only static SVG markup — no user data
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

  async function loadSecurityView() {
    await loadSettings();
    const tokenVal = state.settings.gateway_token || "";
    document.getElementById("sec-gateway-token").value = tokenVal;
    updateCurlExample(tokenVal);
  }

  function updateCurlExample(token) {
    const curlBox = document.getElementById("sec-curl-example");
    const activeProfileId = state.profiles.length > 0 ? state.profiles[0].id : "{profile_id}";
    let authHeader = "";
    if (token) {
      authHeader = `  -H "Authorization: Bearer ${token}" \\\n`;
    }
    curlBox.textContent = `curl -X POST http://127.0.0.1:3100/profiles/${activeProfileId}/v1/chat/completions \\\n` +
      authHeader +
      `  -H "Content-Type: application/json" \\\n` +
      `  -d '{\n` +
      `    "messages": [{"role": "user", "content": "Hello!"}]\n` +
      `  }'`;
  }

  const btnToggleToken = document.getElementById("btn-toggle-sec-token");
  const tokenInput = document.getElementById("sec-gateway-token");
  if (btnToggleToken && tokenInput) {
    btnToggleToken.addEventListener("click", () => {
      if (tokenInput.type === "password") {
        tokenInput.type = "text";
        btnToggleToken.textContent = "Hide";
      } else {
        tokenInput.type = "password";
        btnToggleToken.textContent = "Show";
      }
    });
  }

  const btnGenToken = document.getElementById("btn-gen-sec-token");
  if (btnGenToken && tokenInput) {
    btnGenToken.addEventListener("click", () => {
      const chars = "abcdef0123456789";
      let suffix = "";
      for (let i = 0; i < 32; i++) {
        suffix += chars.charAt(Math.floor(Math.random() * chars.length));
      }
      const freshToken = `sk-${suffix}`;
      tokenInput.value = freshToken;
      updateCurlExample(freshToken);
      tokenInput.type = "text";
      btnToggleToken.textContent = "Hide";
    });
  }

  const btnSaveSecurity = document.getElementById("btn-save-security");
  if (btnSaveSecurity && tokenInput) {
    btnSaveSecurity.addEventListener("click", async () => {
      btnSaveSecurity.disabled = true;
      const originalText = btnSaveSecurity.textContent;
      btnSaveSecurity.textContent = " Saving...";
      const tokenVal = tokenInput.value.trim();
      try {
        state.settings = await apiCall("/api/settings/security", "POST", { gateway_token: tokenVal });
        updateCurlExample(tokenVal);
        alert("Security settings saved successfully! Gateway Bearer token is active across all profiles.");
      } catch (err) {
        alert("Failed to save security settings: " + err.message);
      } finally {
        btnSaveSecurity.textContent = originalText;
        btnSaveSecurity.disabled = false;
      }
    });
  }
});
