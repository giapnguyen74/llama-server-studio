import { h, state, apiCall } from "./state.js";

const profileForm = document.getElementById("profile-form");
const deleteProfileBtn = document.getElementById("btn-delete-profile");
const newProfileBtn = document.getElementById("btn-create-new-profile");
const profileListContainer = document.getElementById("profiles-list-container");
const profileListView = document.getElementById("profiles-list-view");
const profileDetailView = document.getElementById("profiles-detail-view");
const simplePortPolicy = document.getElementById("simple-port-policy");
const groupFixedPort = document.getElementById("group-fixed-port");

function showListView() {
  if (profileListView) profileListView.style.display = "";
  if (profileDetailView) profileDetailView.style.display = "none";
}

function showDetailView() {
  if (profileListView) profileListView.style.display = "none";
  if (profileDetailView) profileDetailView.style.display = "";
}

const backBtn = document.getElementById("btn-back-to-profiles-list");
if (backBtn) {
  backBtn.addEventListener("click", showListView);
}

// Apply model-embedded defaults when the model selection changes in the profile builder
export function applyModelDefaults(modelId) {
  const model = state.models.find(m => m.id === modelId);
  if (!model) return;

  const ctxSelect = document.getElementById("simple-ctx");
  if (ctxSelect && model.context_length) {
    const ctxVal = String(model.context_length);
    let found = false;
    for (const opt of ctxSelect.options) {
      if (opt.value === ctxVal) { found = true; break; }
    }
    if (!found) {
      const opt = document.createElement("option");
      opt.value = ctxVal;
      opt.textContent = `${model.context_length.toLocaleString()} (model native)`;
      ctxSelect.insertBefore(opt, ctxSelect.options[1]);
    }
    ctxSelect.value = ctxVal;
  }

  const nglInput = document.getElementById("simple-ngl");
  if (nglInput && !nglInput.value) {
    nglInput.value = "all";
  }

  updateCLIPreview();
  updateVRAMEstimate();
}

const modelSelectElem = document.getElementById("profile-model");
if (modelSelectElem) {
  modelSelectElem.addEventListener("change", (e) => {
    applyModelDefaults(e.target.value);
  });
}

const simpleCtxElem = document.getElementById("simple-ctx");
if (simpleCtxElem) {
  simpleCtxElem.addEventListener("change", updateVRAMEstimate);
}

// Presets Click Binding
document.querySelectorAll(".preset-btn").forEach(btn => {
  btn.addEventListener("click", () => {
    const preset = btn.dataset.preset;
    applyPreset(preset);
  });
});

export function applyPreset(name) {
  // Clean all inputs first
  document.getElementById("simple-ctx").value = "";
  document.getElementById("simple-ngl").value = "";
  document.getElementById("simple-threads").value = "";
  document.getElementById("simple-batch").value = "";
  document.getElementById("simple-parallel").value = "";
  document.getElementById("simple-routing-enabled").checked = true;
  document.getElementById("simple-routing-autostart").checked = false;
  document.getElementById("simple-routing-policy").value = "latest-ready";
  document.getElementById("adv-gpu-device").value = "";
  document.getElementById("adv-gpu-split").value = "";
  document.getElementById("adv-gpu-tensor").value = "";
  document.getElementById("adv-gpu-main").value = "";
  document.getElementById("adv-mem-flash").value = "auto";
  document.getElementById("adv-mem-mmap").value = "auto";
  document.getElementById("adv-mem-mlock").checked = false;
  document.getElementById("adv-mem-cacheprompt").checked = false; // Default false to avoid setting --no-cache-prompt
  document.getElementById("adv-cpu-moe").checked = false;
  document.getElementById("adv-kv-unified").checked = false;
  document.getElementById("adv-cpu-numa").value = "";
  document.getElementById("adv-lora").value = "";
  document.getElementById("adv-spec-draft").value = "";
  document.getElementById("adv-log-file").value = "";
  document.getElementById("adv-log-verbose").checked = false;
  document.getElementById("adv-diag-perf").checked = true; // Kept checked by default
  document.getElementById("adv-workdir").value = "";
  document.getElementById("adv-host").value = "";
  document.getElementById("adv-args").value = "";

  if (name === "balanced") {
    document.getElementById("simple-ctx").value = "8192";
    document.getElementById("simple-ngl").value = "auto";
    document.getElementById("adv-mem-flash").value = "auto";
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-args").value = '["--no-ui", "--metrics", "--jinja"]';
  } else if (name === "cpu") {
    document.getElementById("simple-ngl").value = "0";
    document.getElementById("adv-gpu-device").value = "none";
    document.getElementById("adv-mem-flash").value = "off";
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-args").value = '["--no-ui", "--metrics"]';
  } else if (name === "gpu-offload") {
    document.getElementById("simple-ngl").value = "all";
    document.getElementById("adv-mem-flash").value = "auto";
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-args").value = '["--no-ui", "--metrics"]';
  } else if (name === "long-context") {
    document.getElementById("simple-ctx").value = "32768";
    document.getElementById("simple-parallel").value = "1";
    document.getElementById("adv-mem-cacheprompt").checked = true;
    document.getElementById("adv-args").value = '["--no-ui", "--metrics"]';
  } else if (name === "embedding") {
    document.getElementById("adv-args").value = '["--embedding"]';
  } else if (name === "rerank") {
    document.getElementById("adv-args").value = '["--rerank"]';
  } else if (name === "benchmark") {
    document.getElementById("simple-parallel").value = "1";
    document.getElementById("adv-args").value = '["--temp", "0.0", "--seed", "42"]';
  }

  updateCLIPreview();
  updateVRAMEstimate();
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

if (simplePortPolicy) {
  simplePortPolicy.addEventListener("change", () => {
    groupFixedPort.style.display = simplePortPolicy.value === "fixed" ? "block" : "none";
    updateCLIPreview();
  });
}

// Watch profile form inputs
if (profileForm) {
  profileForm.addEventListener("input", (e) => {
    updateCLIPreview();
  });
}

if (newProfileBtn) {
  newProfileBtn.addEventListener("click", () => {
    initProfileEditor();
  });
}

export function loadProfilesList() {
  if (!profileListContainer) return;

  if (state.profiles.length === 0) {
    profileListContainer.replaceChildren(
      h("div", {class: "empty-state"},
        h("svg", {width:"40",height:"40",viewBox:"0 0 24 24",fill:"none",stroke:"currentColor","stroke-width":"1.5","stroke-linecap":"round","stroke-linejoin":"round",style:"margin-bottom:12px;opacity:0.4;"},
          h("line",{x1:"4",y1:"21",x2:"4",y2:"14"}),h("line",{x1:"4",y1:"10",x2:"4",y2:"3"}),
          h("line",{x1:"12",y1:"21",x2:"12",y2:"12"}),h("line",{x1:"12",y1:"8",x2:"12",y2:"3"}),
          h("line",{x1:"20",y1:"21",x2:"20",y2:"16"}),h("line",{x1:"20",y1:"12",x2:"20",y2:"3"}),
          h("line",{x1:"1",y1:"14",x2:"7",y2:"14"}),h("line",{x1:"9",y1:"8",x2:"15",y2:"8"}),
          h("line",{x1:"17",y1:"16",x2:"23",y2:"16"})
        ),
        h("div", {}, "No saved profiles yet."),
        h("div", {style:"font-size:0.8rem;margin-top:4px;opacity:0.7;"}, "Click ", h("strong",{},"New Profile"), " to create your first serving configuration.")
      )
    );
    showListView();
    return;
  }

  // Build a table of profiles
  const tbody = h("tbody", {});
  state.profiles.forEach(p => {
    const m = state.models.find(mod => mod.id === p.model_id);
    const modelName = m ? m.display_name : "—";
    const args = p.args || [];
    let ngl = "—", ctx = "—";
    for (let i = 0; i < args.length; i++) {
      if (args[i] === "-ngl" && i+1 < args.length) ngl = args[i+1];
      if (args[i] === "-c" && i+1 < args.length) ctx = args[i+1];
    }
    const routingEnabled = p.routing && p.routing.enabled !== false;
    const tr = h("tr", {class:"profile-list-row", id:`prof-row-${p.id}`},
      h("td", {style:"font-weight:600;"},
        h("div", {style:"font-size:0.92rem;"}, p.name),
        p.description ? h("div", {style:"font-size:0.75rem;color:var(--text-dim);margin-top:2px;"}, p.description) : null
      ),
      h("td", {style:"font-family:monospace;font-size:0.8rem;color:var(--text-muted);max-width:220px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;"}, modelName),
      h("td", {}, ngl !== "—" ? h("span", {class:"status-pill yellow", style:"font-size:0.72rem;"}, ngl + " layers") : h("span", {style:"color:var(--text-dim);font-size:0.8rem;"}, "auto")),
      h("td", {}, ctx !== "—" ? h("span", {style:"font-size:0.82rem;"}, parseInt(ctx).toLocaleString() + " tok") : h("span", {style:"color:var(--text-dim);font-size:0.8rem;"}, "default")),
      h("td", {}, routingEnabled
        ? h("span", {class:"status-pill green", style:"font-size:0.72rem;"}, "Proxied")
        : h("span", {class:"status-pill", style:"font-size:0.72rem;background:var(--surface-soft);color:var(--text-dim);"}, "Direct")
      ),
      h("td", {style:"text-align:right;"},
        h("button", {class:"btn btn-secondary btn-sm"},
          h("svg", {class:"btn-icon-svg",viewBox:"0 0 24 24",fill:"none",stroke:"currentColor","stroke-width":"2.2","stroke-linecap":"round","stroke-linejoin":"round"},
            h("path",{d:"M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"}),
            h("path",{d:"M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"})
          ),
          "Edit"
        )
      )
    );
    tr.addEventListener("click", () => selectProfile(p.id));
    tbody.appendChild(tr);
  });

  const table = h("table", {class:"data-table"},
    h("thead", {},
      h("tr", {},
        h("th", {}, "Profile"),
        h("th", {}, "Model"),
        h("th", {}, "GPU Layers"),
        h("th", {}, "Context"),
        h("th", {}, "Gateway"),
        h("th", {style:"text-align:right;"}, "")
      )
    ),
    tbody
  );
  profileListContainer.replaceChildren(table);
  showListView();
}

export function initProfileEditor() {
  showDetailView();
  document.getElementById("profile-editor-title").textContent = "New Serving Profile";
  document.getElementById("edit-profile-id").value = "";
  profileForm.reset();
  
  document.getElementById("simple-ctx").value = "8192";
  document.getElementById("simple-ngl").value = "auto";
  document.getElementById("simple-threads").value = "-1";
  document.getElementById("simple-parallel").value = "-1";
  
  document.getElementById("adv-gpu-device").value = "";
  document.getElementById("adv-gpu-split").value = "";
  document.getElementById("adv-gpu-tensor").value = "";
  document.getElementById("adv-gpu-main").value = "";
  document.getElementById("adv-mem-flash").value = "auto";
  document.getElementById("adv-mem-mmap").value = "auto";
  document.getElementById("adv-mem-mlock").checked = false;
  document.getElementById("adv-mem-cacheprompt").checked = true;
  document.getElementById("adv-cpu-moe").checked = false;
  document.getElementById("adv-kv-unified").checked = false;
  document.getElementById("adv-cpu-numa").value = "";
  document.getElementById("adv-lora").value = "";
  document.getElementById("adv-spec-draft").value = "";
  document.getElementById("adv-log-file").value = "";
  document.getElementById("adv-log-verbose").checked = false;
  document.getElementById("adv-diag-perf").checked = true;
  document.getElementById("adv-workdir").value = "";
  document.getElementById("adv-host").value = "127.0.0.1";
  document.getElementById("adv-args").value = '["--no-ui", "--metrics", "--jinja"]';
  document.getElementById("simple-routing-enabled").checked = true;
  document.getElementById("simple-routing-autostart").checked = false;
  document.getElementById("simple-routing-policy").value = "latest-ready";
  
  const modelSelect = document.getElementById("profile-model");
  if (modelSelect) {
    modelSelect.replaceChildren(
      h("option", {value: ""}, "Select a Model..."),
      ...state.models.filter(m => !m.hidden).map(m => h("option", {value: m.id}, m.display_name))
    );
  }
  
  deleteProfileBtn.style.display = "none";
  groupFixedPort.style.display = "none";
  updateCLIPreview();
  updateVRAMEstimate();
}

window.initProfileEditor = initProfileEditor;

export function selectProfile(profileID) {
  const p = state.profiles.find(prof => prof.id === profileID);
  if (!p) return;

  showDetailView();
  document.getElementById("profile-editor-title").textContent = p.name;
  document.getElementById("edit-profile-id").value = p.id;
  document.getElementById("profile-name").value = p.name;
  document.getElementById("profile-desc").value = p.description || "";

  const modelSelect = document.getElementById("profile-model");
  if (modelSelect) {
    modelSelect.replaceChildren(
      h("option", {value: ""}, "Select a Model..."),
      ...state.models.filter(m => !m.hidden).map(m => h("option", {value: m.id}, m.display_name))
    );
    modelSelect.value = p.model_id;
  }

  let ctx = "", ngl = "", threads = "", batch = "", parallel = "";
  const args = p.args || [];
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
  if (ctxSelect && ctx !== "") {
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
  }
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

  let gpuDevice = "", gpuSplit = "", gpuTensor = "", gpuMain = "";
  let memFlash = "auto", memMmap = "auto", memMlock = false, memCachePrompt = true;
  let cpuNuma = "";
  let lora = "", specDraft = "", logFile = "";
  let logVerbose = false, diagPerf = true;
  let cpuMoe = false, kvUnified = false;

  const parsedFlags = [
    "-c", "-ngl", "-t", "-b", "-np", "-m", "--model", "--host", "--port", "-p",
    "--device", "-sm", "-ts", "-mg", "--flash-attn", "--no-flash-attn", 
    "--mmap", "--no-mmap", "--mlock", "--no-cache-prompt", "--numa",
    "--lora", "--model-draft", "--log-file", "--verbose", "-v", "--log-verbose",
    "--perf", "--no-perf", "--cpu-moe", "--kv-unified", "-kvu"
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
    else if (args[i] === "--cpu-moe") { cpuMoe = true; }
    else if (args[i] === "--kv-unified" || args[i] === "-kvu") { kvUnified = true; }
    else if (parsedFlags.includes(args[i])) {
      if (flagsWithArgs.includes(args[i])) {
        i++;
      }
    } else {
      advArr.push(args[i]);
    }
  }

  document.getElementById("adv-gpu-device").value = gpuDevice;
  document.getElementById("adv-gpu-split").value = gpuSplit;
  document.getElementById("adv-gpu-tensor").value = gpuTensor;
  document.getElementById("adv-gpu-main").value = gpuMain || "";
  document.getElementById("adv-mem-flash").value = memFlash;
  document.getElementById("adv-mem-mmap").value = memMmap;
  document.getElementById("adv-mem-mlock").checked = memMlock;
  document.getElementById("adv-mem-cacheprompt").checked = memCachePrompt;
  document.getElementById("adv-cpu-moe").checked = cpuMoe;
  document.getElementById("adv-kv-unified").checked = kvUnified;
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
  updateVRAMEstimate();
}

window.selectProfile = selectProfile;

export function generateCommandArgs() {
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

  if (document.getElementById("adv-cpu-moe").checked) {
    builtArgs.push("--cpu-moe");
  }

  if (document.getElementById("adv-kv-unified").checked) {
    builtArgs.push("--kv-unified");
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

export function updateCLIPreview() {
  const args = generateCommandArgs();
  const executable = "llama-server";
  
  const render = `${executable} \\\n` + args.map((arg, idx) => {
    let val = arg;
    if (arg.includes(" ") || arg.includes("\\")) {
      val = `"${arg}"`;
    }
    return `  ${val}` + (idx === args.length - 1 ? "" : " \\");
  }).join("\n");

  const elem = document.getElementById("cli-command-text");
  if (elem) elem.textContent = render;
  validateActiveProfile();
}

window.updateCLIPreview = updateCLIPreview;

// Bytes-per-weight for common GGUF quantisation formats.
const QUANT_BPW = {
  "F32": 4.0, "FP32": 4.0,
  "F16": 2.0, "FP16": 2.0, "BF16": 2.0,
  "Q8_0": 1.0,
  "Q6_K": 0.78,
  "Q5_K_M": 0.68, "Q5_K_S": 0.65, "Q5_K": 0.67, "Q5_0": 0.625, "Q5_1": 0.6875,
  "Q4_K_M": 0.57, "Q4_K_S": 0.54, "Q4_K": 0.55, "Q4_0": 0.5, "Q4_1": 0.5625,
  "Q3_K_L": 0.46, "Q3_K_M": 0.44, "Q3_K_S": 0.41, "Q3_K": 0.44,
  "Q2_K": 0.34, "Q2_K_S": 0.31,
  "IQ4_NL": 0.55, "IQ4_XS": 0.52,
  "IQ3_XXS": 0.39, "IQ3_S": 0.42,
  "IQ2_XXS": 0.29, "IQ2_XS": 0.31, "IQ2_S": 0.34, "IQ2_M": 0.36,
  "IQ1_S": 0.22, "IQ1_M": 0.24,
};

export function updateVRAMEstimate() {
  const card    = document.getElementById("vram-estimate-card");
  const modelId = document.getElementById("profile-model").value;
  const ctxSel  = document.getElementById("simple-ctx").value;

  const model = state.models.find(m => m.id === modelId);
  if (!model || !card) {
    if (card) card.style.display = "none";
    return;
  }

  const bpw       = QUANT_BPW[model.quantization] || 0.57;
  const paramsB   = model.size_bytes / (bpw * 1e9);
  const ctxSize   = parseInt(ctxSel) || model.context_length || 4096;

  const weightsGb  = paramsB * bpw;
  const overheadGb = 0.55 + 0.08 * paramsB;
  const baseGb     = weightsGb + overheadGb;

  const L    = model.block_count      || Math.round(paramsB * 4.5);
  const d    = model.embedding_length || Math.round(paramsB * 512);
  const g    = 4;
  const kvGb = (1 * ctxSize * 2 * L * (d / g) * 2) / 1e9;

  const totalGb = baseGb + kvGb;

  card.style.display = "";
  const fmt = v => v.toFixed(2);
  document.getElementById("vram-total").textContent = `~${fmt(totalGb)} GB`;
  document.getElementById("vram-breakdown").textContent =
    `${fmt(weightsGb)} weights + ${fmt(overheadGb)} overhead + ${fmt(kvGb)} KV · ${ctxSize.toLocaleString()} ctx · ~${Math.round(paramsB)}B params`;

  const qbadge = document.getElementById("vram-quant-badge");
  if (qbadge) {
    qbadge.textContent = model.quantization || "Unknown";
    qbadge.style.display = model.quantization ? "" : "none";
  }

}

window.updateVRAMEstimate = updateVRAMEstimate;

export function validateActiveProfile() {
  const nameInput = document.getElementById("profile-name");
  if (!nameInput) return true;

  const name = nameInput.value.trim();
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

  if (ngl === "" || ngl === "auto") {
    diagnostics.push({ severity: "info", message: "GPU Layers set to Auto: llama-server will detect and allocate layers." });
  }
  if (!ctx) {
    diagnostics.push({ severity: "info", message: "Context size set to Default: context window will adapt to GGUF model metadata." });
  }
  if (parallel === "" || parallel === "1") {
    diagnostics.push({ severity: "info", message: "Isolated parallel slots: single session active, minimizing VRAM cache." });
  }

  const consoleBox = document.getElementById("validation-console");
  const container = document.getElementById("validation-errors");
  if (!consoleBox) return true;
  
  if (diagnostics.length === 0) {
    consoleBox.style.display = "none";
    return true;
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
    return false;
  } else {
    saveBtn.disabled = false;
    runBtn.disabled = false;
    saveBtn.style.opacity = "1";
    runBtn.style.opacity = "1";
    saveBtn.style.cursor = "pointer";
    runBtn.style.cursor = "pointer";
    return true;
  }
}

const copyBtn = document.getElementById("btn-copy-cli-command");
if (copyBtn) {
  copyBtn.addEventListener("click", () => {
    const txt = document.getElementById("cli-command-text").textContent;
    navigator.clipboard.writeText(txt);
    alert("Invocations copied to clipboard!");
  });
}

if (profileForm) {
  profileForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    await saveActiveProfile();
  });
}

const saveRunBtn = document.getElementById("btn-save-run-profile");
if (saveRunBtn) {
  saveRunBtn.addEventListener("click", async () => {
    const p = await saveActiveProfile();
    if (p && window.launchServerInstance) {
      await window.launchServerInstance(p.id);
    }
  });
}

export async function saveActiveProfile() {
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

  const args = generateCommandArgs();
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
      result = await apiCall(`/api/profiles/${profileID}`, "PUT", payload);
    } else {
      result = await apiCall("/api/profiles", "POST", payload);
    }
    if (window.loadData) await window.loadData();
    loadProfilesList();
    // Stay on detail view showing the saved profile
    if (result && result.id) selectProfile(result.id);
    alert("Serving Profile saved successfully!");
    return result;
  } catch (err) {
    alert(`Save failed: ${err.message}`);
    return null;
  }
}

if (deleteProfileBtn) {
  deleteProfileBtn.addEventListener("click", async () => {
    const id = document.getElementById("edit-profile-id").value;
    if (confirm("Are you sure you want to delete this profile?")) {
      try {
        await apiCall(`/api/profiles/${id}`, "DELETE");
        if (window.loadData) await window.loadData();
        loadProfilesList();
        showListView();
      } catch (err) {
        alert(err.message);
      }
    }
  });
}

const exportJSONBtn = document.getElementById("btn-export-profile-json");
if (exportJSONBtn) {
  exportJSONBtn.addEventListener("click", () => {
    const id = document.getElementById("edit-profile-id").value;
    if (!id) return;
    window.open(`/api/profiles/${id}/export.json`);
  });
}

const exportSHBtn = document.getElementById("btn-export-profile-sh");
if (exportSHBtn) {
  exportSHBtn.addEventListener("click", () => {
    const id = document.getElementById("edit-profile-id").value;
    if (!id) return;
    window.open(`/api/profiles/${id}/export.sh`);
  });
}
