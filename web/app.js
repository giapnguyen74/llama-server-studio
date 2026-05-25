/* Llama Server Studio - Main Frontend Application Logic */

document.addEventListener("DOMContentLoaded", () => {
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

  // Selectors
  const navButtons = document.querySelectorAll(".nav-btn");
  const sections = document.querySelectorAll(".content-section");
  const modal = document.getElementById("info-modal");
  const modalTitle = document.getElementById("modal-title");
  const modalContent = document.getElementById("modal-content");
  const modalClose = document.getElementById("btn-close-modal");

  // Initial Boot
  initNavigation();
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
      });
    });

    modalClose.addEventListener("click", () => {
      modal.style.display = "none";
    });
  }

  function showModal(title, htmlContent) {
    modalTitle.textContent = title;
    modalContent.innerHTML = htmlContent;
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

  async function apiCall(url, method = "GET", body = null) {
    try {
      const options = { method, headers: {} };
      if (body) {
        options.headers["Content-Type"] = "application/json";
        options.body = JSON.stringify(body);
      }
      const response = await fetch(url, options);
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
      serversList.innerHTML = allDisplay.map(srv => {
        const prof = state.profiles.find(p => p.id === srv.profile_id);
        const name = prof ? prof.name : "Unknown Profile";
        const statusClass = srv.status === "healthy" ? "green" : (srv.status === "crashed" ? "red" : "yellow");
        return `
          <div class="stat-card" style="border: 1px solid rgba(255,255,255,0.05); margin-bottom: 8px; cursor: pointer;" onclick="document.querySelector('[data-target=servers]').click(); setTimeout(() => selectServerInLifecycle('${srv.id}'), 100);">
            <div style="display:flex; justify-content:space-between; align-items:center;">
              <div>
                <strong style="display:block; font-size:0.95rem;">${name}</strong>
                <span style="font-size:0.75rem; color:var(--text-dim);">PID: ${srv.pid} | Port: ${srv.port}</span>
              </div>
              <span class="status-pill ${statusClass}">${srv.status}</span>
            </div>
          </div>
        `;
      }).join("");
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
        dashBenchList.innerHTML = completed.map(b => {
          const prof = state.profiles.find(p => p.id === b.profile_id);
          const name = prof ? prof.name : "Profile";
          const speed = b.result && b.result.avg_tokens_per_sec ? parseFloat(b.result.avg_tokens_per_sec).toFixed(2) : "0";
          return `
            <div style="padding:12px; border-bottom: 1px solid rgba(255,255,255,0.03); display:flex; justify-content:space-between; align-items:center;">
              <div>
                <strong style="display:block; font-size:0.9rem;">${name}</strong>
                <span style="font-size:0.7rem; color:var(--text-dim);">${formatDate(b.started_at)}</span>
              </div>
              <span style="font-size:1.1rem; font-weight:800; color:var(--accent-pink);">${speed} T/s</span>
            </div>
          `;
        }).join("");
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
    const textNode = [...btnRescan.childNodes].find(n => n.nodeType === Node.TEXT_NODE);
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

    filterQuant.innerHTML = '<option value="">All Quantizations</option>' + quants.map(q => `<option value="${q}">${q}</option>`).join("");
    filterArch.innerHTML = '<option value="">All Architectures</option>' + archs.map(a => `<option value="${a}">${a}</option>`).join("");
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

    tbody.innerHTML = filtered.map(m => {
      const caps = m.capabilities.map(c => `<span class="status-pill purple">${c}</span>`).join(" ");
      return `
        <tr>
          <td><strong style="color:var(--text-primary); cursor:pointer;" onclick="viewModelDetails('${m.id}')">${m.display_name}</strong></td>
          <td><span style="font-size:0.8rem; color:var(--text-muted);">${m.source}</span></td>
          <td><span class="status-pill yellow">${m.quantization || "Unknown"}</span></td>
          <td>${formatBytes(m.size_bytes)}</td>
          <td><code style="color:var(--accent-cyan); font-size:0.8rem;">${m.architecture || "Unknown"}</code></td>
          <td>${m.context_length || 2048}</td>
          <td>${caps}</td>
          <td>
            <div style="display:flex; gap:8px;">
              <button class="btn btn-sm btn-primary" onclick="createProfileFromModel('${m.id}')">
                <svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><circle cx="12" cy="12" r="3"></circle><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"></path></svg>
                Build
              </button>
              <button class="btn btn-sm btn-secondary" onclick="viewModelDetails('${m.id}')">
                <svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><circle cx="12" cy="12" r="10"></circle><line x1="12" y1="16" x2="12" y2="12"></line><line x1="12" y1="8" x2="12.01" y2="8"></line></svg>
                Info
              </button>
              <button class="btn btn-sm btn-danger" onclick="hideModelFromCatalog('${m.id}')">
                <svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24"></path><line x1="1" y1="1" x2="23" y2="23"></line></svg>
                Hide
              </button>
            </div>
          </td>
        </tr>
      `;
    }).join("");
  }

  window.viewModelDetails = function(modelID) {
    const m = state.models.find(mod => mod.id === modelID);
    if (!m) return;
    
    // Build metadata rows
    let rows = `
      <div class="metadata-grid">
        <div class="meta-row"><span class="lbl">File Name</span><span class="val">${m.display_name}</span></div>
        <div class="meta-row"><span class="lbl">Source</span><span class="val">${m.source}</span></div>
        <div class="meta-row"><span class="lbl">Size</span><span class="val">${formatBytes(m.size_bytes)}</span></div>
        <div class="meta-row"><span class="lbl">Mod Time</span><span class="val">${formatDate(m.modified_at)}</span></div>
        <div class="meta-row"><span class="lbl">Architecture</span><span class="val">${m.architecture || "Unknown"}</span></div>
        <div class="meta-row"><span class="lbl">Quantization</span><span class="val">${m.quantization || "Unknown"}</span></div>
        <div class="meta-row"><span class="lbl">Context Window</span><span class="val">${m.context_length} tokens</span></div>
        <div class="meta-row"><span class="lbl">Tokenizer model</span><span class="val">${m.tokenizer_model || "GGUF Native"}</span></div>
      </div>
      <div class="meta-row" style="margin-top:16px;"><span class="lbl">Absolute Path</span><code class="val" style="background:rgba(0,0,0,0.3); padding:8px; border-radius:4px; font-size:0.75rem;">${m.path}</code></div>
    `;

    if (m.chat_template) {
      rows += `
        <div class="meta-row" style="margin-top:16px;">
          <span class="lbl">Chat Template</span>
          <pre style="background:rgba(0,0,0,0.5); padding:8px; border-radius:4px; max-height:120px; overflow-y:auto; font-size:0.7rem; color:var(--text-muted);">${escapeHtml(m.chat_template)}</pre>
        </div>
      `;
    }

    showModal("Model Details & Header Metadata", rows);
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

  function escapeHtml(text) {
    return text
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;")
      .replace(/"/g, "&quot;")
      .replace(/'/g, "&#039;");
  }

  // --- 5. PROFILE BUILDER SECTION ---

  const profileListContainer = document.getElementById("profiles-list-container");
  const profileEditorContainer = document.getElementById("profile-editor-container");
  const newProfileBtn = document.getElementById("btn-create-new-profile");
  const deleteProfileBtn = document.getElementById("btn-delete-profile");
  const profileForm = document.getElementById("profile-form");
  const simplePortPolicy = document.getElementById("simple-port-policy");
  const groupFixedPort = document.getElementById("group-fixed-port");

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
    "profile-model", "simple-ctx", "simple-ngl", "simple-threads", 
    "simple-batch", "simple-parallel", "simple-port-policy", "simple-fixed-port",
    "adv-args", "adv-workdir", "adv-host",
    "adv-gpu-device", "adv-gpu-split", "adv-gpu-tensor", "adv-gpu-main",
    "adv-mem-flash", "adv-mem-mmap", "adv-mem-mlock", "adv-mem-cacheprompt",
    "adv-cpu-numa", "adv-lora", "adv-spec-draft", "adv-log-file",
    "adv-log-verbose", "adv-diag-perf"
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
    profileListContainer.innerHTML = state.profiles.map(p => {
      const m = state.models.find(mod => mod.id === p.model_id);
      const modelName = m ? m.display_name : "No Model Selected";
      return `
        <button class="profile-item-btn" id="prof-btn-${p.id}" onclick="selectProfile('${p.id}')">
          <strong class="profile-item-title">${p.name}</strong>
          <span class="profile-item-meta">${modelName}</span>
        </button>
      `;
    }).join("");

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
    document.getElementById("adv-args").value = "";
    
    // Populate model options
    const modelSelect = document.getElementById("profile-model");
    modelSelect.innerHTML = '<option value="">Select a Model...</option>' + 
      state.models.filter(m => !m.hidden).map(m => `<option value="${m.id}">${m.display_name}</option>`).join("");
    
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
    modelSelect.innerHTML = '<option value="">Select a Model...</option>' + 
      state.models.filter(m => !m.hidden).map(m => `<option value="${m.id}">${m.display_name}</option>`).join("");
    modelSelect.value = p.model_id;

    // Parse simple settings from args
    let ctx = 2048, ngl = 0, threads = 4, batch = 512, parallel = 1;
    
    const args = p.args;
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-c" && i + 1 < args.length) ctx = parseInt(args[i+1]);
      if (args[i] === "-ngl" && i + 1 < args.length) ngl = parseInt(args[i+1]);
      if (args[i] === "-t" && i + 1 < args.length) threads = parseInt(args[i+1]);
      if (args[i] === "-b" && i + 1 < args.length) batch = parseInt(args[i+1]);
      if (args[i] === "-np" && i + 1 < args.length) parallel = parseInt(args[i+1]);
    }

    const ctxSelect = document.getElementById("simple-ctx");
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

    deleteProfileBtn.style.display = "inline-flex";
    updateCLIPreview();
  };

  // Live CLI Preview Generator
  function generateCommandArgs() {
    const modelID = document.getElementById("profile-model").value;
    const m = state.models.find(mod => mod.id === modelID);
    const modelPath = m ? m.resolved_path : "[model-file-path]";

    const ctx = document.getElementById("simple-ctx").value;
    const ngl = document.getElementById("simple-ngl").value;
    const threads = document.getElementById("simple-threads").value;
    const batch = document.getElementById("simple-batch").value;
    const parallel = document.getElementById("simple-parallel").value;
    const portPolicy = simplePortPolicy.value;
    const fixedPort = document.getElementById("simple-fixed-port").value;
    const host = document.getElementById("adv-host").value || "127.0.0.1";

    const builtArgs = [
      "-m", modelPath,
      "-c", ctx,
      "-ngl", ngl,
      "-t", threads,
      "-b", batch,
      "-np", parallel,
      "--host", host
    ];

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
    const executable = state.settings.llama_server_bin || "llama-server";
    
    // Highlight elements
    const render = `${executable} \\\n` + args.map((arg, idx) => {
      let val = arg;
      if (arg.includes(" ") || arg.includes("\\")) {
        val = `"${arg}"`;
      }
      return `  ${val}` + (idx === args.length - 1 ? "" : " \\");
    }).join("\n");

    document.getElementById("cli-command-text").textContent = render;
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

  const selectLifecycleSrv = document.getElementById("server-lifecycle-select");
  const lifecycleDetails = document.getElementById("lifecycle-details-container");
  const lifecycleEmpty = document.getElementById("lifecycle-empty-state");

  const btnStopSrv = document.getElementById("btn-stop-srv");
  const btnRestartSrv = document.getElementById("btn-restart-srv");

  selectLifecycleSrv.addEventListener("change", () => {
    selectServerInLifecycle(selectLifecycleSrv.value);
  });

  async function launchServerInstance(profileID) {
    try {
      alert("Launching llama-server child process. Check Server Lifecycle tab to monitor status...");
      const srvRecord = await apiCall(`/api/profiles/${profileID}/start`, "POST");
      await loadData();
      
      // Go to servers tab and show status
      document.querySelector("[data-target=servers]").click();
      setTimeout(() => {
        selectServerInLifecycle(srvRecord.id);
      }, 200);
    } catch (err) {
      alert(`Start failed: ${err.message}`);
    }
  }

  function loadServerLifecycleView() {
    // Populate select items
    selectLifecycleSrv.innerHTML = '<option value="">Select a Running or Crashed Server...</option>' +
      state.servers.map(s => {
        const p = state.profiles.find(prof => prof.id === s.profile_id);
        const name = p ? p.name : "Serving Profile";
        return `<option value="${s.id}">${name} (PID: ${s.pid || "Stopped"} | status: ${s.status})</option>`;
      }).join("");

    if (state.activeServerId) {
      selectLifecycleSrv.value = state.activeServerId;
      selectServerInLifecycle(state.activeServerId);
    } else if (state.servers.length > 0) {
      // Default select first
      selectLifecycleSrv.value = state.servers[0].id;
      selectServerInLifecycle(state.servers[0].id);
    } else {
      lifecycleDetails.style.display = "none";
      lifecycleEmpty.style.display = "block";
    }
  }

  window.selectServerInLifecycle = function(serverID) {
    state.activeServerId = serverID;
    selectLifecycleSrv.value = serverID;
    
    const s = state.servers.find(srv => srv.id === serverID);
    if (!s) {
      lifecycleDetails.style.display = "none";
      lifecycleEmpty.style.display = "block";
      clearIntervals();
      return;
    }

    lifecycleDetails.style.display = "block";
    lifecycleEmpty.style.display = "none";

    // Setup values
    document.getElementById("srv-status-badge").className = `tel-val status-pill ${s.status === "healthy" ? "green" : (s.status === "crashed" ? "red" : "yellow")}`;
    document.getElementById("srv-status-badge").textContent = s.status;
    document.getElementById("srv-pid-val").textContent = s.pid || "-";
    document.getElementById("srv-port-val").textContent = s.port;
    document.getElementById("stable-route-url").textContent = `/profiles/${s.profile_id}/v1/completions`;

    // Start logs & stats loops
    clearIntervals();
    pollServerLogs(serverID);
    pollServerTelemetry(serverID);
    state.logPollInterval = setInterval(() => pollServerLogs(serverID), 1000);
    state.statsPollInterval = setInterval(() => pollServerTelemetry(serverID), 1500);
  };

  function clearIntervals() {
    if (state.logPollInterval) clearInterval(state.logPollInterval);
    if (state.statsPollInterval) clearInterval(state.statsPollInterval);
  }

  async function pollServerLogs(serverID) {
    try {
      const logs = await apiCall(`/api/servers/${serverID}/logs`);
      const consoleBox = document.getElementById("server-log-console");
      
      if (logs.length === 0) {
        consoleBox.innerHTML = `<div class="terminal-line system-line">[System] Log empty. Server starting...</div>`;
      } else {
        consoleBox.innerHTML = logs.map(line => {
          let c = "terminal-line";
          if (line.includes("[System]") || line.includes("LLAMA SERVER STUDIO")) c = "terminal-line system-line";
          if (line.includes("error") || line.includes("fail") || line.includes("ERR")) c = "terminal-line err-line";
          return `<div class="${c}">${escapeHtml(line)}</div>`;
        }).join("");
        // Auto Scroll to bottom
        consoleBox.scrollTop = consoleBox.scrollHeight;
      }
    } catch {
      document.getElementById("server-log-console").innerHTML = `<div class="terminal-line err-line">[System] Failed to read disk logs.</div>`;
    }
  }

  async function pollServerTelemetry(serverID) {
    try {
      // Reload server config silently to refresh status
      state.servers = await apiCall("/api/servers");
      const s = state.servers.find(srv => srv.id === serverID);
      if (s) {
        document.getElementById("srv-status-badge").className = `tel-val status-pill ${s.status === "healthy" ? "green" : (s.status === "crashed" ? "red" : "yellow")}`;
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

  // Stop click
  btnStopSrv.addEventListener("click", async () => {
    if (state.activeServerId) {
      const icon = btnStopSrv.querySelector(".btn-icon-svg");
      if (icon) icon.classList.add("spin");
      const textNode = [...btnStopSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE);
      if (textNode) textNode.textContent = " Stopping...";
      btnStopSrv.disabled = true;
      try {
        await apiCall(`/api/servers/${state.activeServerId}/stop`, "POST");
        await loadData();
        selectServerInLifecycle(state.activeServerId);
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
      const textNode = [...btnRestartSrv.childNodes].find(n => n.nodeType === Node.TEXT_NODE);
      if (textNode) textNode.textContent = " Restarting...";
      btnRestartSrv.disabled = true;
      try {
        await apiCall(`/api/servers/${state.activeServerId}/restart`, "POST");
        await loadData();
        selectServerInLifecycle(state.activeServerId);
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
    const textNode = [...testBtn.childNodes].find(n => n.nodeType === Node.TEXT_NODE);
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
      testOutputBox.innerHTML = `<span class="err-line">Request Failed: ${err.message}</span>`;
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
    const textNode = [...btnTriggerBench.childNodes].find(n => n.nodeType === Node.TEXT_NODE);
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

      tbody.innerHTML = state.benchmarks.map(b => {
        const p = state.profiles.find(prof => prof.id === b.profile_id);
        const m = state.models.find(mod => mod.id === b.model_id);
        
        const profName = p ? p.name : "Profile";
        const modelName = m ? m.display_name : "GGUF Model";
        
        const speed = b.result && b.result.avg_tokens_per_sec ? `${parseFloat(b.result.avg_tokens_per_sec).toFixed(2)} T/s` : "-";
        const latency = b.result && b.result.avg_latency_ms ? `${parseFloat(b.result.avg_latency_ms).toFixed(0)}ms` : "-";

        return `
          <tr>
            <td><code style="font-size:0.8rem;">${b.id.substring(6)}</code></td>
            <td><strong>${profName}</strong></td>
            <td><span style="font-size:0.8rem; color:var(--text-muted);">${modelName}</span></td>
            <td><span style="font-size:0.8rem; font-style:italic;">"${b.prompt.substring(0, 30)}..."</span></td>
            <td><strong style="color:var(--accent-pink);">${speed}</strong></td>
            <td>${latency}</td>
            <td><span class="status-pill ${b.status === "completed" ? "green" : "red"}">${b.status}</span></td>
            <td>${formatDate(b.started_at)}</td>
            <td>
              <button class="btn btn-sm btn-danger" onclick="deleteBenchmarkRecord('${b.id}')">
                <svg class="btn-icon-svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" style="width:12px; height:12px;"><polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path></svg>
                Delete
              </button>
            </td>
          </tr>
        `;
      }).join("");
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
});
