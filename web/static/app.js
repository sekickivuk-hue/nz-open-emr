"use strict";

const SYSTEM_NHI = "https://standards.digital.health.nz/ns/nhi-id";

const state = {
  actor: null,
  selectedPatient: null,
};

const $ = (id) => document.getElementById(id);

async function api(path, opts = {}) {
  const headers = Object.assign(
    { "Content-Type": "application/json" },
    state.actor ? { "X-Actor-HPI": state.actor } : {},
    opts.headers || {}
  );
  const resp = await fetch(path, Object.assign({}, opts, { headers }));
  const body = await resp.json().catch(() => ({}));
  if (!resp.ok) {
    const msg =
      (body.issue && body.issue[0] && body.issue[0].diagnostics) ||
      body.error || resp.statusText;
    throw new Error(msg);
  }
  return body;
}

// UTF-8-safe base64 helpers (chunk-safe for any note length).
function b64encode(str) {
  const bytes = new TextEncoder().encode(str);
  let bin = "";
  for (const b of bytes) bin += String.fromCharCode(b);
  return btoa(bin);
}
function b64decode(b64) {
  const bin = atob(b64);
  const bytes = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
  return new TextDecoder().decode(bytes);
}

function el(tag, cls, text) {
  const e = document.createElement(tag);
  if (cls) e.className = cls;
  if (text !== undefined) e.textContent = text;
  return e;
}

// ---- actors ----
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

// ---- NHI preview ----
function chosenFormat() {
  return document.querySelector('input[name="fmt"]:checked').value;
}
async function refreshNHIPreview() {
  $("nhi-preview").textContent = "…";
  const g = await api(`/demo/generate-nhi?format=${chosenFormat()}`);
  $("nhi-preview").textContent = g.nhi;
}

// ---- patients ----
async function loadPatients() {
  const bundle = await api("/fhir/r4/Patient");
  const list = $("patient-list");
  list.innerHTML = "";
  for (const entry of bundle.entry || []) {
    const p = entry.resource;
    const li = el("li", "clickable");
    const name = `${p.name[0].given[0]} ${p.name[0].family}`;
    li.appendChild(el("span", null, name));
    li.appendChild(el("code", "badge", p.identifier[0].value));
    li.onclick = () => selectPatient(p, name);
    list.appendChild(li);
  }
}

async function selectPatient(p, name) {
  state.selectedPatient = p.id;
  $("selected-patient").textContent = name;
  $("patient-meta").textContent =
    `NHI ${p.identifier[0].value}` + (p.birthDate ? ` · born ${p.birthDate}` : "");
  $("new-note").hidden = false;
  await loadNotes();
}

async function loadNotes() {
  if (!state.selectedPatient) return;
  const bundle = await api(`/fhir/r4/DocumentReference?patient=${state.selectedPatient}`);
  const list = $("note-list");
  list.innerHTML = "";
  for (const entry of bundle.entry || []) {
    const d = entry.resource;
    const li = el("li");
    li.appendChild(el("div", "note-text", b64decode(d.content[0].attachment.data)));
    const author = d.author && d.author[0] ? d.author[0].display : "unknown";
    li.appendChild(el("small", "muted", `${author} · ${new Date(d.date).toLocaleString()}`));
    list.appendChild(li);
  }
}

// ---- audit ----
async function pollAudit() {
  try {
    const bundle = await api("/fhir/r4/AuditEvent?_count=30");
    const list = $("audit-list");
    list.innerHTML = "";
    for (const entry of bundle.entry || []) {
      const a = entry.resource;
      const li = el("li");
      const who = a.agent[0].who.display;
      const what = a.entity && a.entity[0] ? a.entity[0].what.reference : "";
      const hash = (a.extension && a.extension[0] ? a.extension[0].valueString : "").slice(0, 12);
      li.appendChild(el("span", `action action-${a.action}`, a.action));
      li.appendChild(el("span", null, ` ${what} — ${who} `));
      li.appendChild(el("code", "hash", hash));
      list.appendChild(li);
    }
  } catch (e) {
    /* audit poll is best-effort; UI stays usable */
  }
}

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

// ---- forms ----
function wireForms() {
  for (const radio of document.querySelectorAll('input[name="fmt"]')) {
    radio.onchange = refreshNHIPreview;
  }
  $("regen-nhi").onclick = refreshNHIPreview;

  $("new-patient").onsubmit = async (ev) => {
    ev.preventDefault();
    $("patient-error").textContent = "";
    const nhi = $("nhi-preview").textContent;
    const patient = {
      resourceType: "Patient",
      identifier: [{ use: "official", system: SYSTEM_NHI, value: nhi }],
      name: [{ family: $("family").value.trim(), given: [$("given").value.trim()] }],
    };
    if ($("dob").value) patient.birthDate = $("dob").value;
    try {
      await api("/fhir/r4/Patient", { method: "POST", body: JSON.stringify(patient) });
      $("new-patient").reset();
      refreshNHIPreview();
      // Projection lags writes by up to ~200ms; refresh twice.
      setTimeout(loadPatients, 300);
      setTimeout(loadPatients, 1000);
    } catch (e) {
      $("patient-error").textContent = e.message;
    }
  };

  $("new-note").onsubmit = async (ev) => {
    ev.preventDefault();
    $("note-error").textContent = "";
    const doc = {
      resourceType: "DocumentReference",
      status: "current",
      subject: { reference: `Patient/${state.selectedPatient}` },
      content: [{ attachment: { contentType: "text/plain", data: b64encode($("note-text").value) } }],
    };
    try {
      await api("/fhir/r4/DocumentReference", { method: "POST", body: JSON.stringify(doc) });
      $("note-text").value = "";
      setTimeout(loadNotes, 300);
      setTimeout(loadNotes, 1000);
    } catch (e) {
      $("note-error").textContent = e.message;
    }
  };

  $("verify").onclick = verifyChain;
}

// ---- boot ----
(async function boot() {
  wireForms();
  await loadActors();
  await refreshNHIPreview();
  await loadPatients();
  await pollAudit();
  setInterval(pollAudit, 2000);
})().catch((e) => {
  document.body.insertAdjacentHTML(
    "afterbegin",
    `<div class="error">Failed to start: ${e.message}</div>`
  );
});
