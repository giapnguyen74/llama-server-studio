import { h, state, apiCall, formatBytes, formatDate, formatETA } from "./state.js";

// Hugging Face Hub Client Module
export const hfState = {
  files: [],              // last fetched RepoFile[]
  repoID: "",             // last fetched repo
  showAll: false,         // checkbox state — false means default *.gguf filter
  selected: new Set(),    // set of selected filenames (View B)
  pollHandle: null,       // setInterval handle for View C polling
  hfTokenConfigured: true,
  lastBytes: {},          // maps filename -> { bytes: number, time: number, speed: number }
  readme: "",             // markdown/text README
  readmeCollapsed: true,  // README card state
};

// Public entry point — invoked from the nav switcher.
export async function loadHFHub() {
  let job;
  try {
    job = await apiCall("/api/hf/jobs/current");
  } catch (err) {
    const root = document.getElementById("hfhub-root");
    if (root) {
      root.replaceChildren(h("div", {class: "empty-state"}, `Failed to load Hugging Face Hub: ${err.message}`));
    }
    return;
  }
  if (job && (job.status === "running" || job.status === "cancelling")) {
    renderHFHubActive(job);
  } else {
    stopHFHubPolling();
    renderHFHubBrowse(job);
  }
}

export function stopHFHubPolling() {
  if (hfState.pollHandle) {
    clearInterval(hfState.pollHandle);
    hfState.pollHandle = null;
  }
}

// ---- View A: Browse ------------------------------------------------------
export function renderHFHubBrowse(lastJob) {
  const root = document.getElementById("hfhub-root");
  if (!root) return;
  root.replaceChildren();

  // Input + Fetch button
  const repoInput = h("input", {
    type: "text",
    id: "hfhub-repo-input",
    placeholder: "Paste a repo ID, e.g. unsloth/Qwen3.6-27B-GGUF",
    style: "flex: 1;",
  });
  const fetchBtn = h("button", {class: "btn btn-primary"}, "Fetch files");

  const inputRow = h("div", {
    class: "filter-panel glass-card",
    style: "display:flex; gap:12px; align-items:center; padding:18px 20px;"
  }, repoInput, fetchBtn);

  const errorBox = h("div", {style: "display:none; margin-bottom:16px; padding:12px 16px; background:rgba(185,28,28,0.08); border:1px solid rgba(185,28,28,0.25); border-radius:8px; color:var(--accent-red); font-size:0.88rem;"});

  fetchBtn.addEventListener("click", () => doFetchRepo(repoInput.value.trim(), fetchBtn, errorBox));
  repoInput.addEventListener("keydown", (e) => {
    if (e.key === "Enter") doFetchRepo(repoInput.value.trim(), fetchBtn, errorBox);
  });

  root.append(inputRow, errorBox);

  // HF_TOKEN warning banner
  if (state.settings && state.settings.hf_token_configured === false) {
    root.append(h("div", {
      style: "margin-bottom: 16px; padding: 12px 16px; background: rgba(180,83,9,0.08); border: 1px solid rgba(180,83,9,0.25); border-radius: 8px; font-size: 0.88rem;"
    },
      h("strong", {style: "color: var(--accent-yellow);"}, "HF_TOKEN not configured. "),
      "Public repos still work, but rate limits are tighter and gated repos will fail. Set HF_TOKEN in your environment before launching the studio to lift these limits."
    ));
  }

  // Last job summary (completed / cancelled / failed / partial)
  if (lastJob && lastJob.status && lastJob.status !== "running" && lastJob.status !== "cancelling") {
    root.append(renderHFLastJobSummary(lastJob));
  }

  // Resumable jobs (.part files left on disk).
  renderResumableSection(root);

  // Download History Log Panel
  renderDownloadHistorySection(root);
}

async function doFetchRepo(repoID, fetchBtn, errorBox) {
  if (!repoID) {
    errorBox.textContent = "Please enter a repo id.";
    errorBox.style.display = "block";
    return;
  }
  errorBox.style.display = "none";
  fetchBtn.disabled = true;
  const origLabel = fetchBtn.textContent;
  fetchBtn.textContent = "Fetching…";
  try {
    const meta = await apiCall(`/api/hf/repo?repo=${encodeURIComponent(repoID)}`);
    hfState.repoID = meta.repo_id;
    hfState.files = meta.files || [];
    hfState.showAll = false;
    hfState.selected = new Set();
    hfState.hfTokenConfigured = meta.hf_token_configured !== false;
    
    // Asynchronously fetch readme
    hfState.readme = "";
    hfState.readmeCollapsed = true;
    try {
      const readmeRes = await apiCall(`/api/hf/repo/readme?repo=${encodeURIComponent(repoID)}`);
      hfState.readme = readmeRes.readme || "";
    } catch (e) {
      console.warn("Failed to fetch README", e);
    }

    renderHFHubPicker();
  } catch (err) {
    errorBox.textContent = err.message;
    errorBox.style.display = "block";
  } finally {
    fetchBtn.disabled = false;
    fetchBtn.textContent = origLabel;
  }
}

async function renderResumableSection(root) {
  let resumable = [];
  try {
    resumable = await apiCall("/api/hf/jobs/resumable");
  } catch {
    return;
  }
  if (!resumable || resumable.length === 0) return;

  const card = h("div", {class: "glass-card", style: "padding: 20px; margin-top: 24px;"},
    h("h3", {style: "margin-bottom: 12px; font-size: 1rem;"}, "Resume previous downloads"),
    h("p", {class: "field-hint", style: "margin-bottom: 16px;"},
      "These repos have partially-downloaded files (.part) left on disk. Resuming restarts the byte stream from where it left off using HTTP Range.")
  );

  resumable.forEach(rj => {
    const totalBytes = (rj.files || []).reduce((acc, f) => acc + (f.size_bytes || 0), 0);
    const loadedBytes = (rj.files || []).reduce((acc, f) => acc + (f.bytes_loaded || 0), 0);
    const row = h("div", {style: "display:flex; justify-content:space-between; align-items:center; padding:12px; border-top:1px solid var(--border-soft);"},
      h("div", {},
        h("strong", {style: "display:block; font-size: 0.92rem;"}, rj.repo_id),
        h("span", {style: "font-size: 0.78rem; color: var(--text-dim);"},
          `${rj.files.length} file(s) · ${formatBytes(loadedBytes)} of ${formatBytes(totalBytes)} on disk`)
      ),
      h("button", {class: "btn btn-secondary btn-sm"}, "Resume")
    );
    const btn = row.querySelector("button");
    btn.addEventListener("click", () => resumeJob(rj));
    card.append(row);
  });

  root.append(card);
}

async function renderDownloadHistorySection(root) {
  const stateData = await apiCall("/api/state");
  const history = stateData.hf_history || [];
  if (history.length === 0) return;

  const card = h("div", {class: "glass-card", style: "padding: 20px; margin-top: 24px; max-height: 400px; overflow-y: auto;"},
    h("h3", {style: "margin-bottom: 12px; font-size: 1rem; display: flex; align-items: center; justify-content: space-between;"}, 
      h("span", {}, "Download history"),
      h("span", {class: "badge badge-sm", style: "background:rgba(255,255,255,0.05); color:var(--text-muted);"}, `${history.length} jobs`)
    )
  );

  history.reverse().forEach(rj => {
    const statusClass = rj.status === "completed" ? "green" : (rj.status === "failed" ? "red" : "yellow");
    const row = h("div", {style: "padding: 12px; border-top: 1px solid var(--border-soft); display: flex; justify-content: space-between; align-items: center;"},
      h("div", {},
        h("strong", {style: "display:block; font-size: 0.88rem;"}, rj.repo_id),
        h("span", {style: "font-size: 0.75rem; color: var(--text-dim);"},
          `${rj.files_count} files · ${formatBytes(rj.totalBytes)} · Finished: ${formatDate(rj.finished_at)}`)
      ),
      h("span", {class: `status-pill ${statusClass}`}, rj.status)
    );
    card.append(row);
  });

  root.append(card);
}

async function resumeJob(rj) {
  const payload = {
    repo: rj.repo_id,
    files: rj.files.map(f => ({
      filename: f.filename,
      size: f.size_bytes,
      is_lfs: f.is_lfs,
      sha256: f.sha256 || "",
    })),
  };
  try {
    await apiCall("/api/hf/jobs", "POST", payload);
    loadHFHub();
  } catch (err) {
    alert(`Failed to resume: ${err.message}`);
  }
}

// ---- View B: File picker -------------------------------------------------
export function renderHFHubPicker() {
  const root = document.getElementById("hfhub-root");
  if (!root) return;
  root.replaceChildren();

  // Repo header
  const backBtn = h("button", {class: "btn btn-secondary btn-sm"}, "Back");
  backBtn.addEventListener("click", () => loadHFHub());
  root.append(h("div", {class: "glass-card", style: "padding: 16px 20px; margin-bottom: 16px; display:flex; justify-content:space-between; align-items:center;"},
    h("div", {},
      h("strong", {style: "display: block; font-size: 0.95rem;"}, hfState.repoID),
      h("span", {style: "font-size: 0.78rem; color: var(--text-dim);"}, `${hfState.files.length} files in repository`)
    ),
    backBtn
  ));

  // Collapsible README Card
  if (hfState.readme) {
    const readmeBody = h("div", {
      style: `padding: 16px 20px; font-size: 0.85rem; line-height: 1.6; border-top: 1px solid var(--border-soft); display: ${hfState.readmeCollapsed ? "none" : "block"}; max-height: 250px; overflow-y: auto; white-space: pre-wrap; font-family: var(--font-sans); color: var(--text-dim);`
    }, hfState.readme);

    const toggleBtn = h("button", {
      class: "btn btn-sm btn-secondary",
      style: "display: inline-flex; align-items: center; gap: 6px; font-size: 0.8rem;"
    }, hfState.readmeCollapsed ? "Show Model Card README" : "Hide Model Card README");

    toggleBtn.addEventListener("click", () => {
      hfState.readmeCollapsed = !hfState.readmeCollapsed;
      readmeBody.style.display = hfState.readmeCollapsed ? "none" : "block";
      toggleBtn.textContent = hfState.readmeCollapsed ? "Show Model Card README" : "Hide Model Card README";
    });

    const readmeCard = h("div", {
      class: "glass-card",
      style: "margin-bottom: 16px; padding: 12px 16px;"
    }, 
      h("div", {style: "display: flex; justify-content: space-between; align-items: center;"},
        h("span", {style: "font-size: 0.9rem; font-weight: 500;"}, "Model Description & Quantization table"),
        toggleBtn
      ),
      readmeBody
    );
    root.append(readmeCard);
  }

  // Filter / select-all / total bar
  const filterCb = h("input", {type: "checkbox", id: "hfhub-show-all"});
  if (hfState.showAll) filterCb.checked = true;
  filterCb.addEventListener("change", () => {
    hfState.showAll = filterCb.checked;
    renderHFHubPicker();
  });

  const selectAllLink = h("a", {href: "#", style: "color: var(--accent-cyan); font-size: 0.85rem; cursor: pointer;"}, "Select all visible");

  const totalSpan = h("span", {id: "hfhub-total-summary", style: "font-size: 0.85rem; color: var(--text-muted);"}, "Estimated total: 0 Bytes");

  root.append(h("div", {class: "filter-panel glass-card", style: "padding: 14px 20px; margin-bottom: 16px;"},
    h("div", {style: "display:flex; gap:18px; align-items:center; flex-wrap:wrap;"},
      h("label", {style: "display:flex; align-items:center; gap:8px; font-size: 0.88rem; margin: 0;"},
        filterCb, "Only show .gguf files"
      ),
      selectAllLink,
      h("span", {style: "flex:1;"}),
      totalSpan
    )
  ));

  // Visible files
  const visible = hfState.files.filter(f => hfState.showAll || /\.gguf$/i.test(f.filename));
  if (visible.length === 0) {
    root.append(h("div", {class: "empty-state"},
      hfState.showAll
        ? "Repository has no files."
        : "Repository has no .gguf files. Uncheck the filter to see all files."));
  } else {
    const tbody = document.createElement("tbody");
    tbody.id = "hfhub-files-tbody";

    visible.forEach(f => {
      const cb = h("input", {type: "checkbox", "data-filename": f.filename});
      if (hfState.selected.has(f.filename)) cb.checked = true;
      cb.addEventListener("change", () => {
        if (cb.checked) hfState.selected.add(f.filename);
        else hfState.selected.delete(f.filename);
        updatePickerSummary();
      });

      const ext = (f.filename.split(".").pop() || "").toLowerCase();
      tbody.append(h("tr", {},
        h("td", {style: "width: 40px;"}, cb),
        h("td", {}, h("code", {style: "font-size: 0.82rem; color: var(--text-primary);"}, f.filename)),
        h("td", {style: "width: 120px; text-align: right;"}, formatBytes(f.size)),
        h("td", {style: "width: 80px;"},
          h("span", {class: "status-pill " + (ext === "gguf" ? "purple" : "yellow")}, ext || "?"))
      ));
    });

    const tableCard = h("div", {class: "glass-card table-card", style: "margin-bottom: 16px;"},
      h("table", {class: "data-table"},
        h("thead", {}, h("tr", {},
          h("th", {}, ""),
          h("th", {}, "Filename"),
          h("th", {style: "text-align: right;"}, "Size"),
          h("th", {}, "Type")
        )),
        tbody
      )
    );
    root.append(tableCard);
  }

  selectAllLink.addEventListener("click", (e) => {
    e.preventDefault();
    visible.forEach(f => hfState.selected.add(f.filename));
    renderHFHubPicker();
  });

  const startBtn = h("button", {class: "btn btn-primary", id: "hfhub-start-btn"}, "Start download");
  const cancelLink = h("a", {href: "#", style: "color: var(--text-muted); margin-left: 16px;"}, "Back");
  cancelLink.addEventListener("click", (e) => { e.preventDefault(); loadHFHub(); });
  startBtn.addEventListener("click", () => doStartJob(startBtn));

  root.append(h("div", {style: "display: flex; align-items: center; margin-top: 8px;"}, startBtn, cancelLink));

  updatePickerSummary();
}

function updatePickerSummary() {
  const total = hfState.files
    .filter(f => hfState.selected.has(f.filename))
    .reduce((acc, f) => acc + (f.size || 0), 0);
  const span = document.getElementById("hfhub-total-summary");
  if (span) {
    span.textContent = `Estimated total: ${formatBytes(total)} (${hfState.selected.size} selected)`;
  }
  const startBtn = document.getElementById("hfhub-start-btn");
  if (startBtn) {
    startBtn.disabled = hfState.selected.size === 0;
  }
}

async function doStartJob(startBtn) {
  if (hfState.selected.size === 0) return;
  const payload = {
    repo: hfState.repoID,
    files: hfState.files
      .filter(f => hfState.selected.has(f.filename))
      .map(f => ({
        filename: f.filename,
        size: f.size,
        is_lfs: f.is_lfs,
        sha256: f.sha256 || "",
      })),
  };

  startBtn.disabled = true;
  startBtn.textContent = "Starting…";
  try {
    await apiCall("/api/hf/jobs", "POST", payload);
    loadHFHub();
  } catch (err) {
    alert(`Failed to start download: ${err.message}`);
    startBtn.disabled = false;
    startBtn.textContent = "Start download";
  }
}

// ---- View C: Active job --------------------------------------------------
export function renderHFHubActive(job) {
  const root = document.getElementById("hfhub-root");
  if (!root) return;
  root.replaceChildren();

  // Unified Job Card
  const unifiedCard = h("div", {class: "glass-card", style: "padding: 20px; margin-bottom: 16px;"},
    // Job info header
    h("div", {style: "display:flex; justify-content:space-between; align-items:center; border-bottom:1px solid var(--border-soft); padding-bottom:16px; margin-bottom:16px;"},
      h("div", {},
        h("strong", {style: "display: block; font-size: 1.05rem; color: var(--text-primary);"}, job.repo_id),
        h("span", {style: "font-size: 0.78rem; color: var(--text-dim);"}, `Job ${job.id} · started ${formatDate(job.started_at)}`)
      ),
      h("span", {class: "status-pill " + statusPillClass(job.status)}, job.status)
    )
  );

  // File rows nested and indented inside
  const fileList = h("div", {style: "display: flex; flex-direction: column; gap: 8px;"});
  (job.files || []).forEach(f => fileList.append(renderHFFileRow(f)));
  unifiedCard.append(fileList);

  root.append(unifiedCard);

  // Cancel button
  if (job.status === "running") {
    const cancelBtn = h("button", {class: "btn btn-danger"}, "Cancel job");
    cancelBtn.addEventListener("click", () => doCancelJob(cancelBtn));
    root.append(h("div", {style: "display: flex; justify-content: flex-end; margin-top: 16px;"}, cancelBtn));
  } else if (job.status === "cancelling") {
    root.append(h("div", {class: "empty-state", style: "padding: 12px; margin-top: 16px;"}, "Cancelling — waiting for the worker to release the active byte stream…"));
  }

  if (!hfState.pollHandle) {
    hfState.pollHandle = setInterval(refreshActiveJob, 1000);
  }
}

function renderHFFileRow(f) {
  const pct = (f.progress || 0).toFixed(1);
  const loaded = formatBytes(f.bytes_loaded || 0);
  const total = formatBytes(f.size_bytes || 0);

  let speedText = "";
  let etaText = "";

  if (f.status === "running" || f.status === "downloading") {
    const now = Date.now();
    const prev = hfState.lastBytes[f.filename];
    const currentBytes = f.bytes_loaded || 0;
    
    if (prev && prev.time) {
      const deltaBytes = currentBytes - prev.bytes;
      const deltaTimeSecs = (now - prev.time) / 1000;
      if (deltaTimeSecs > 0 && deltaBytes >= 0) {
        const speed = deltaBytes / deltaTimeSecs; // bytes per second
        
        // Rolling window smooth logic over last updates
        const nextSpeed = prev.speed > 0 ? (prev.speed * 0.7 + speed * 0.3) : speed;
        hfState.lastBytes[f.filename] = { bytes: currentBytes, time: now, speed: nextSpeed };
        
        if (nextSpeed > 0) {
          const remainingBytes = f.size_bytes - currentBytes;
          const etaSecs = remainingBytes / nextSpeed;
          speedText = ` · ${formatBytes(nextSpeed)}/s`;
          etaText = ` · ETA: ${formatETA(etaSecs)}`;
        }
      }
    } else {
      hfState.lastBytes[f.filename] = { bytes: currentBytes, time: now, speed: 0 };
    }
  }

  const errorLine = f.error
    ? h("span", {style: "font-size: 0.75rem; color: var(--accent-red); display: block; margin-top: 4px;"}, f.error)
    : null;

  return h("div", {
    style: "padding: 14px 18px; background: rgba(255,255,255,0.015); border: 1px solid rgba(255,255,255,0.03); border-radius: 8px; margin-left: 12px;"
  },
    h("div", {style: "display:flex; justify-content:space-between; align-items:center; gap:12px;"},
      h("div", {style: "flex:1; min-width: 0;"},
        h("strong", {style: "display: block; font-size: 0.88rem; overflow:hidden; text-overflow:ellipsis; white-space:nowrap; color: var(--text-primary);"}, f.filename),
        h("span", {style: "font-size: 0.75rem; color: var(--text-dim);"}, `${loaded} / ${total} (${pct}%)${speedText}${etaText}`),
        errorLine
      ),
      h("span", {class: "status-pill " + statusPillClass(f.status)}, f.status)
    ),
    h("div", {style: "width: 100%; height: 6px; background: rgba(255,255,255,0.03); border-radius: 3px; overflow: hidden; margin-top: 10px;"},
      h("div", {style: `width: ${pct}%; height: 100%; background: var(--accent-purple); transition: width 0.3s ease;`})
    )
  );
}


function statusPillClass(status) {
  switch (status) {
    case "running":     return "yellow";
    case "completed":   return "green";
    case "skipped":     return "green";
    case "failed":      return "red";
    case "cancelled":   return "red";
    case "cancelling":  return "yellow";
    case "partial":     return "yellow";
    default:            return "purple";
  }
}

async function refreshActiveJob() {
  let job;
  try {
    job = await apiCall("/api/hf/jobs/current");
  } catch {
    return;
  }
  if (!job || (job.status !== "running" && job.status !== "cancelling")) {
    stopHFHubPolling();
    if (window.loadData) await window.loadData();
    renderHFHubBrowse(job);
    return;
  }
  
  if (document.getElementById("section-hfhub").classList.contains("active")) {
    renderHFHubActive(job);
  }
}

async function doCancelJob(cancelBtn) {
  if (!confirm("Cancel the active download? Partial files will be kept on disk so you can resume later.")) return;
  cancelBtn.disabled = true;
  cancelBtn.textContent = "Cancelling…";
  try {
    await apiCall("/api/hf/jobs/current/cancel", "POST");
  } catch (err) {
    alert(`Cancel failed: ${err.message}`);
    cancelBtn.disabled = false;
    cancelBtn.textContent = "Cancel job";
  }
}

function renderHFLastJobSummary(job) {
  const ok = (job.files || []).filter(f => f.status === "completed" || f.status === "skipped").length;
  const total = (job.files || []).length;
  return h("div", {class: "glass-card", style: "padding: 14px 18px; margin-bottom: 16px;"},
    h("div", {style: "display:flex; justify-content:space-between; align-items:center;"},
      h("div", {},
        h("strong", {style: "display:block; font-size: 0.9rem;"}, `Last job: ${job.repo_id}`),
        h("span", {style: "font-size: 0.78rem; color: var(--text-dim);"}, `${ok}/${total} files · finished ${formatDate(job.finished_at)}`)
      ),
      h("span", {class: "status-pill " + statusPillClass(job.status)}, job.status)
    )
  );
}
