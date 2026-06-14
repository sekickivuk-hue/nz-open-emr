"use strict";

const SYSTEM_NHI = "https://standards.digital.health.nz/ns/nhi-id";

const state = { actor: null, selectedPatient: null, selectedEncounter: null, patients: [] };

const $ = (id) => document.getElementById(id);

// --- api ---------------------------------------------------------------
async function api(path, opts = {}) {
  const headers = Object.assign(
    { "Content-Type": "application/json" },
    state.actor ? { "X-Actor-HPI": state.actor } : {},
    opts.headers || {}
  );
  const resp = await fetch(path, Object.assign({}, opts, { headers }));
  const body = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const msg = (body.issue && body.issue[0] && body.issue[0].diagnostics) || body.error || resp.statusText;
    throw new Error(msg);
  }
  return body;
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// --- tabs --------------------------------------------------------------
function switchTab(name) {
  document.querySelectorAll('.tab').forEach(t => t.classList.remove('active'));
  document.querySelector(`[data-panel="${name}"]`).classList.add('active');
  document.querySelectorAll('.panel').forEach(p => p.classList.remove('active'));
  document.getElementById('panel-' + name).classList.add('active');
  if (name === 'encounters') refreshEncounterPanel();
  if (name === 'allergies') refreshAllergyPanel();
  if (name === 'problems') refreshProblemPanel();
}

// --- actors ------------------------------------------------------------
async function loadActors() {
  const actors = await api("/demo/actors");
  const sel = $("actor");
  sel.innerHTML = "";
  for (const a of actors) {
    const o = el("option", null, `${a.name} — ${a.role}`);
    o.value = a.hpi;
    sel.appendChild(o);
  }
  state.actor = actors[0].hpi;
  sel.onchange = () => { state.actor = sel.value; };
}

// --- NHI preview -------------------------------------------------------
function chosenFormat() {
  return document.querySelector('input[name="fmt"]:checked').value;
}
async function refreshNHIPreview() {
  $("nhi-preview").textContent = "…";
  const g = await api(`/demo/generate-nhi?format=${chosenFormat()}`);
  $("nhi-preview").textContent = g.nhi;
}

// --- patients ----------------------------------------------------------
async function loadPatients() {
  const bundle = await api("/fhir/r4/Patient");
  state.patients = (bundle.entry || []).map(e => e.resource);
  refreshPatientSelects();
}

function refreshPatientSelects() {
  for (const selId of ['enc-patient', 'alg-patient', 'prb-patient']) {
    const sel = $(selId); if (!sel) continue;
    const val = sel.value;
    sel.innerHTML = '<option value="">--</option>';
    for (const p of state.patients) {
      const name = p.name && p.name[0] ? `${p.name[0].given[0]} ${p.name[0].family}` : p.id;
      const o = el("option", null, `${name} (${p.identifier[0].value})`);
      o.value = p.id;
      sel.appendChild(o);
    }
    sel.value = val;
  }
}

// --- encounters --------------------------------------------------------
async function refreshEncounterPanel() {
  const pid = $("enc-patient").value;
  if (!pid) { $("encounter-list").innerHTML = ""; return; }
  const bundle = await api(`/fhir/r4/Encounter?patient=${pid}`);
  const list = $("encounter-list");
  list.innerHTML = "";
  for (const entry of bundle.entry || []) {
    const e = entry.resource;
    const li = el("li");
    const cls = e.class && e.class.code ? e.class.code : "?";
    li.appendChild(el("span", null, `${cls} — ${e.status}`));
    if (e.period) li.appendChild(el("small", "muted", ` ${e.period.start?.slice(0,10) || ''}`));
    li.onclick = () => selectEncounter(e);
    list.appendChild(li);
  }
}

async function selectEncounter(e) {
  state.selectedEncounter = e.id;
  $("enc-detail").hidden = false;
  $("enc-id").textContent = e.id;
  refreshDiagnoses(e.id);
}

async function refreshDiagnoses(eid) {
  const enc = await api(`/fhir/r4/Encounter/${eid}`);
  const list = $("diag-list");
  list.innerHTML = "";
  if (!enc.diagnosis) return;
  for (const d of enc.diagnosis) {
    const li = el("li");
    li.textContent = d.condition?.display || d.condition?.reference || '(diagnosis)';
    list.appendChild(li);
  }
}

// --- allergies ---------------------------------------------------------
async function refreshAllergyPanel() {
  const pid = $("alg-patient").value;
  if (!pid) { $("allergy-list").innerHTML = ""; return; }
  const bundle = await api(`/fhir/r4/AllergyIntolerance?patient=${pid}`);
  const list = $("allergy-list");
  list.innerHTML = "";
  for (const entry of bundle.entry || []) {
    const a = entry.resource;
    const sub = a.code?.coding?.[0]?.display || "(unknown)";
    const sev = a.reaction?.[0]?.severity || "";
    const li = el("li");
    li.appendChild(el("span", "sev-" + sev, sev ? sev[0].toUpperCase() : "?"));
    li.appendChild(el("span", null, ` ${sub}`));
    if (a.reaction?.[0]?.manifestation?.[0]?.text)
      li.appendChild(el("small", "muted", ` — ${a.reaction[0].manifestation[0].text}`));
    list.appendChild(li);
  }
}

// --- problems ----------------------------------------------------------
async function refreshProblemPanel() {
  const pid = $("prb-patient").value;
  if (!pid) { $("problem-list").innerHTML = ""; return; }
  const bundle = await api(`/fhir/r4/Condition?patient=${pid}`);
  const list = $("problem-list");
  list.innerHTML = "";
  for (const entry of bundle.entry || []) {
    const c = entry.resource;
    const display = c.code?.coding?.[0]?.display || c.code?.coding?.[0]?.code || "(unknown)";
    const status = c.clinicalStatus?.coding?.[0]?.code || "?";
    const li = el("li");
    li.appendChild(el("span", "status-" + status, status));
    li.appendChild(el("span", null, ` ${display}`));
    list.appendChild(li);
  }
}

// --- audit -------------------------------------------------------------
async function verifyChain() {
  const out = $("verify-result");
  out.className = "";
  out.textContent = "verifying…";
  const rep = await (await fetch("/audit/verify")).json();
  if (rep.ok) {
    out.className = "ok";
    out.textContent = `✔ chain intact — ${rep.checked} events verified`;
  } else {
    out.className = "broken";
    out.textContent = `✘ CHAIN BROKEN at seq ${rep.brokenSeq}: ${rep.reason}`;
  }
}

// --- forms -------------------------------------------------------------
function wireForms() {
  // Tab clicks
  document.querySelectorAll('.tab').forEach(btn => {
    btn.onclick = () => switchTab(btn.dataset.panel);
  });

  // NHI format
  for (const radio of document.querySelectorAll('input[name="fmt"]'))
    radio.onchange = refreshNHIPreview;
  $("regen-nhi").onclick = refreshNHIPreview;

  // Patient select changes
  $("enc-patient").onchange = refreshEncounterPanel;
  $("alg-patient").onchange = refreshAllergyPanel;
  $("prb-patient").onchange = refreshProblemPanel;

  // Create patient
  $("new-patient").onsubmit = async (ev) => {
    ev.preventDefault();
    $("patient-error").textContent = "";
    const nhi = $("nhi-preview").textContent;
    const gender = $("gender").value;
    const exts = [];
    for (const o of $("ethnicity").selectedOptions)
      exts.push({ url: "http://hl7.org.nz/fhir/StructureDefinition/nz-ethnicity", valueCodeableConcept: { coding: [{ code: o.value }] } });
    for (const o of $("iwi").selectedOptions)
      exts.push({ url: "http://hl7.org.nz/fhir/StructureDefinition/nz-iwi", valueCodeableConcept: { coding: [{ code: o.value }] } });
    const patient = {
      resourceType: "Patient",
      identifier: [{ use: "official", system: SYSTEM_NHI, value: nhi }],
      name: [{ family: $("family").value.trim(), given: [$("given").value.trim()] }],
    };
    if ($("dob").value) patient.birthDate = $("dob").value;
    if (gender) patient.gender = gender;
    if (exts.length) patient.extension = exts;
    try {
      await api("/fhir/r4/Patient", { method: "POST", body: JSON.stringify(patient) });
      $("new-patient").reset();
      refreshNHIPreview();
      setTimeout(loadPatients, 300);
      setTimeout(loadPatients, 1000);
    } catch (e) { $("patient-error").textContent = e.message; }
  };

  // Open encounter
  $("new-encounter").onsubmit = async (ev) => {
    ev.preventDefault();
    const pid = $("enc-patient").value;
    if (!pid) return;
    try {
      await api("/fhir/r4/Encounter", { method: "POST", body: JSON.stringify({
        resourceType: "Encounter", status: "in-progress",
        class: { code: $("enc-class").value },
        subject: { reference: `Patient/${pid}` }
      })});
      setTimeout(refreshEncounterPanel, 400);
    } catch (e) { alert(e.message); }
  };

  // Add diagnosis
  $("add-diagnosis").onsubmit = async (ev) => {
    ev.preventDefault();
    if (!state.selectedEncounter) return;
    try {
      await api(`/fhir/r4/Encounter/${state.selectedEncounter}/diagnosis`, { method: "POST", body: JSON.stringify({
        display: $("diag-display").value, code: $("diag-code").value, type: $("diag-type").value, rank: 1
      })});
      $("diag-display").value = ""; $("diag-code").value = "";
      setTimeout(() => refreshDiagnoses(state.selectedEncounter), 300);
    } catch (e) { $("enc-error").textContent = e.message; }
  };

  // Close encounter
  $("close-encounter").onclick = async () => {
    if (!state.selectedEncounter) return;
    try {
      await api(`/fhir/r4/Encounter/${state.selectedEncounter}/close`, { method: "POST", body: JSON.stringify({ disposition: "home" }) });
      state.selectedEncounter = null;
      $("enc-detail").hidden = true;
      setTimeout(refreshEncounterPanel, 400);
    } catch (e) { $("enc-error").textContent = e.message; }
  };

  // Record allergy
  $("new-allergy").onsubmit = async (ev) => {
    ev.preventDefault();
    const pid = $("alg-patient").value;
    if (!pid) return;
    try {
      await api("/fhir/r4/AllergyIntolerance", { method: "POST", body: JSON.stringify({
        resourceType: "AllergyIntolerance",
        code: { coding: [{ display: $("alg-substance").value }] },
        patient: { reference: `Patient/${pid}` },
        reaction: [{ manifestation: [{ text: $("alg-reaction").value }], severity: $("alg-severity").value }]
      })});
      $("alg-substance").value = ""; $("alg-reaction").value = "";
      setTimeout(refreshAllergyPanel, 400);
    } catch (e) { alert(e.message); }
  };

  // Add problem
  $("new-problem").onsubmit = async (ev) => {
    ev.preventDefault();
    const pid = $("prb-patient").value;
    if (!pid) return;
    try {
      await api("/fhir/r4/Condition", { method: "POST", body: JSON.stringify({
        resourceType: "Condition",
        code: { coding: [{ display: $("prb-display").value }] },
        subject: { reference: `Patient/${pid}` },
        category: [{ coding: [{ code: $("prb-category").value }] }]
      })});
      $("prb-display").value = "";
      setTimeout(refreshProblemPanel, 400);
    } catch (e) { alert(e.message); }
  };

  $("verify").onclick = verifyChain;
}

// --- init --------------------------------------------------------------
(async function init() {
  await loadActors();
  await refreshNHIPreview();
  await loadPatients();
  wireForms();
  setInterval(async () => { await loadPatients(); }, 5000);
})();
