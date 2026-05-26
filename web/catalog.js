import { h, state, apiCall, formatBytes, formatDate, showModal } from "./state.js";

export function loadModelsTable() {
  populateModelDropdownFilters();
  filterModels();
}

const searchInput = document.getElementById("model-search");
const filterSource = document.getElementById("filter-model-source");
const filterQuant = document.getElementById("filter-model-quant");
const filterArch = document.getElementById("filter-model-arch");
const btnRescan = document.getElementById("btn-rescan-models");

if (searchInput) {
  searchInput.addEventListener("input", filterModels);
  filterSource.addEventListener("change", filterModels);
  filterQuant.addEventListener("change", filterModels);
  filterArch.addEventListener("change", filterModels);
}

if (btnRescan) {
  btnRescan.addEventListener("click", async () => {
    const icon = btnRescan.querySelector(".btn-icon-svg");
    if (icon) icon.classList.add("spin");
    const textNode = [...btnRescan.childNodes].find(n => n.nodeType === Node.TEXT_NODE && n.textContent.trim() !== "");
    if (textNode) textNode.textContent = " Scanning Disk...";
    btnRescan.disabled = true;
    try {
      await apiCall("/api/models/rescan", "POST");
      if (window.loadData) await window.loadData();
      loadModelsTable();
    } catch (err) {
      alert(`Rescan failed: ${err.message}`);
    } finally {
      if (icon) icon.classList.remove("spin");
      if (textNode) textNode.textContent = " Rescan Directories";
      btnRescan.disabled = false;
    }
  });
}

export function populateModelDropdownFilters() {
  const quants = [...new Set(state.models.map(m => m.quantization).filter(Boolean))];
  const archs = [...new Set(state.models.map(m => m.architecture).filter(Boolean))];

  if (filterQuant) {
    filterQuant.replaceChildren(
      h("option", {value: ""}, "All Quantizations"),
      ...quants.map(q => h("option", {value: q}, q))
    );
  }
  if (filterArch) {
    filterArch.replaceChildren(
      h("option", {value: ""}, "All Architectures"),
      ...archs.map(a => h("option", {value: a}, a))
    );
  }
}

export function filterModels() {
  const query = searchInput ? searchInput.value.toLowerCase() : "";
  const source = filterSource ? filterSource.value : "";
  const quant = filterQuant ? filterQuant.value : "";
  const arch = filterArch ? filterArch.value : "";

  const filtered = state.models.filter(m => {
    if (m.hidden) return false;
    const matchesSearch = m.display_name.toLowerCase().includes(query) || (m.repo_id || "").toLowerCase().includes(query) || (m.architecture || "").toLowerCase().includes(query);
    const matchesSource = !source || m.source === source;
    const matchesQuant = !quant || m.quantization === quant;
    const matchesArch = !arch || m.architecture === arch;
    return matchesSearch && matchesSource && matchesQuant && matchesArch;
  });

  const tbody = document.getElementById("models-table-body");
  if (!tbody) return;

  if (filtered.length === 0) {
    tbody.innerHTML = `<tr><td colspan="8" class="empty-state">No models matching the selected criteria.</td></tr>`;
    return;
  }

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
  const SVG_DELETE = `<polyline points="3 6 5 6 21 6"></polyline><path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2"></path>`;

  tbody.replaceChildren(
    ...filtered.map(m => {
      const capsCell = document.createElement("td");
      m.capabilities.forEach(c => {
        capsCell.append(h("span", {class: "status-pill purple"}, c), " ");
      });

      const actionsDiv = h("div", {style: "display:flex; gap:8px;"},
        modelBtn("btn btn-sm btn-primary",   "build",  m.id, SVG_BUILD, "Build"),
        modelBtn("btn btn-sm btn-secondary", "info",   m.id, SVG_INFO,  "Info"),
        modelBtn("btn btn-sm btn-secondary", "hide",   m.id, SVG_HIDE,  "Hide"),
        modelBtn("btn btn-sm btn-danger",    "delete", m.id, SVG_DELETE, "Delete")
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
    pre.textContent = m.chat_template;
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
      if (window.loadData) await window.loadData();
      loadModelsTable();
    } catch (err) {
      alert(err.message);
    }
  }
};

window.deleteModelFromCatalog = async function(modelID) {
  const m = state.models.find(mod => mod.id === modelID);
  if (!m) return;
  const confirmText = `Are you sure you want to delete this model from the catalog?\n\n` +
                      `Name: ${m.display_name}\n` +
                      `Path: ${m.path}\n\n` +
                      `Choose OK to proceed.`;
  if (confirm(confirmText)) {
    const deleteFile = confirm("Do you also want to physically delete the GGUF file from your disk to free space?");
    try {
      await apiCall(`/api/models/${modelID}?delete_file=${deleteFile}`, "DELETE");
      if (window.loadData) await window.loadData();
      loadModelsTable();
    } catch (err) {
      alert(`Deletion failed: ${err.message}`);
    }
  }
};


window.createProfileFromModel = function(modelID) {
  document.querySelector("[data-target=profiles]").click();
  if (window.initProfileEditor) {
    setTimeout(() => {
      window.initProfileEditor();
      document.getElementById("profile-model").value = modelID;
      if (window.updateCLIPreview) window.updateCLIPreview();
      if (window.updateVRAMEstimate) window.updateVRAMEstimate();
    }, 100);
  }
};

export function escapeHtml(text) {
  return String(text ?? "").replace(/[&<>"']/g, c => ({
    "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
  }[c]));
}
