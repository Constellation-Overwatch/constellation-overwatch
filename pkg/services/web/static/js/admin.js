"use strict";

// ---------------------------------------------------------------------------
// Tab switching
// ---------------------------------------------------------------------------

function switchTab(name) {
  var tabs = document.querySelectorAll(".admin-tab");
  var panels = document.querySelectorAll(".admin-panel");

  tabs.forEach(function (t, i) {
    var names = ["users", "invites", "apikeys"];
    if (names[i] === name) {
      t.classList.add("active");
    } else {
      t.classList.remove("active");
    }
  });

  panels.forEach(function (p) {
    if (p.id === "panel-" + name) {
      p.classList.remove("hidden");
    } else {
      p.classList.add("hidden");
    }
  });
}

// ---------------------------------------------------------------------------
// Safe DOM helpers
// ---------------------------------------------------------------------------

function createCell(text) {
  var td = document.createElement("td");
  td.textContent = text || "";
  return td;
}

function createBadgeCell(text, cls) {
  var td = document.createElement("td");
  var span = document.createElement("span");
  span.className = "badge " + (cls || "");
  span.textContent = text;
  td.appendChild(span);
  return td;
}

function createCodeCell(text) {
  var td = document.createElement("td");
  var code = document.createElement("code");
  code.textContent = text || "---";
  td.appendChild(code);
  return td;
}

function setTableState(tbody, cols, msg, cls) {
  tbody.textContent = "";
  var tr = document.createElement("tr");
  var td = document.createElement("td");
  td.setAttribute("colspan", cols);
  td.className = cls || "loading-cell";
  td.textContent = msg;
  tr.appendChild(td);
  tbody.appendChild(tr);
}

// ---------------------------------------------------------------------------
// Users
// ---------------------------------------------------------------------------

async function loadUsers() {
  var tbody = document.getElementById("users-tbody");
  setTableState(tbody, 4, "Loading...", "loading-cell");

  try {
    var res = await fetch("/api/admin/users");
    var json = await res.json();
    var users = json.data || [];

    tbody.textContent = "";

    if (users.length === 0) {
      setTableState(tbody, 4, "No users found", "empty-cell");
      return;
    }

    users.forEach(function (u) {
      var tr = document.createElement("tr");
      tr.appendChild(createCell(u.email));
      tr.appendChild(createBadgeCell(u.role, "badge-role"));

      var passkey = u.needs_passkey_setup ? "PENDING" : "REGISTERED";
      var passkeyCls = u.needs_passkey_setup ? "badge-warning" : "badge-ok";
      tr.appendChild(createBadgeCell(passkey, passkeyCls));

      var lastLogin = u.last_login
        ? new Date(u.last_login).toLocaleString()
        : "Never";
      tr.appendChild(createCell(lastLogin));
      tbody.appendChild(tr);
    });
  } catch (err) {
    setTableState(tbody, 4, "Failed to load users", "error-cell");
  }
}

// ---------------------------------------------------------------------------
// Invites
// ---------------------------------------------------------------------------

async function createInvite() {
  var email = document.getElementById("invite-email").value.trim();
  var role = document.getElementById("invite-role").value;
  if (!email) return;

  var resultEl = document.getElementById("invite-result");

  try {
    var res = await fetch("/api/admin/invites", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: email, role: role }),
    });

    var json = await res.json();
    resultEl.textContent = "";
    resultEl.classList.remove("hidden", "success", "error");

    if (res.ok) {
      var data = json.data || json;
      var token = data.token || "";
      var inviteURL = window.location.origin + "/invite/" + token;

      resultEl.classList.add("success");

      var strong = document.createElement("strong");
      strong.textContent = "Invite created! ";
      resultEl.appendChild(strong);
      resultEl.appendChild(
        document.createTextNode("Share this link (shown once):")
      );
      resultEl.appendChild(document.createElement("br"));

      var code = document.createElement("code");
      code.textContent = inviteURL;
      resultEl.appendChild(code);

      var copyBtn = document.createElement("button");
      copyBtn.className = "btn-copy";
      copyBtn.textContent = "Copy";
      copyBtn.addEventListener("click", function () {
        navigator.clipboard.writeText(inviteURL);
        copyBtn.textContent = "Copied!";
        setTimeout(function () {
          copyBtn.textContent = "Copy";
        }, 2000);
      });
      resultEl.appendChild(copyBtn);

      document.getElementById("invite-email").value = "";
    } else {
      resultEl.classList.add("error");
      resultEl.textContent = json.message || "Failed to create invite";
    }
  } catch (err) {
    resultEl.textContent = "";
    resultEl.classList.remove("hidden", "success", "error");
    resultEl.classList.add("error");
    resultEl.textContent = "Error: " + err.message;
  }
}

// ---------------------------------------------------------------------------
// API Keys — scope mutual exclusion
// ---------------------------------------------------------------------------

function initScopeToggles() {
  var apiSelector = document.getElementById("scope-selector");
  var natsSelector = document.getElementById("nats-scope-selector");
  if (!apiSelector || !natsSelector) return;

  var adminCb = apiSelector.querySelector('input[value="admin"]');
  var natsAllCb = natsSelector.querySelector('input[value="nats:all"]');

  function setGroupDisabled(container, allCheckbox, disabled) {
    container.querySelectorAll('input[type="checkbox"]').forEach(function (cb) {
      if (cb === allCheckbox) return;
      cb.disabled = disabled;
      if (disabled) cb.checked = false;
      var chip = cb.parentElement.querySelector(".scope-chip");
      if (chip) {
        if (disabled) chip.classList.add("scope-chip-disabled");
        else chip.classList.remove("scope-chip-disabled");
      }
    });
  }

  if (adminCb) {
    adminCb.addEventListener("change", function () {
      setGroupDisabled(apiSelector, adminCb, adminCb.checked);
    });
    // apply initial state
    if (adminCb.checked) setGroupDisabled(apiSelector, adminCb, true);
  }

  if (natsAllCb) {
    natsAllCb.addEventListener("change", function () {
      setGroupDisabled(natsSelector, natsAllCb, natsAllCb.checked);
    });
    if (natsAllCb.checked) setGroupDisabled(natsSelector, natsAllCb, true);
  }
}

function getSelectedScopes() {
  var checkboxes = document.querySelectorAll(
    "#scope-selector input[type=checkbox]:checked, #nats-scope-selector input[type=checkbox]:checked"
  );
  var scopes = [];
  checkboxes.forEach(function (cb) {
    scopes.push(cb.value);
  });
  return scopes;
}

async function loadAPIKeys() {
  var tbody = document.getElementById("apikeys-tbody");
  setTableState(tbody, 5, "Loading...", "loading-cell");

  try {
    var res = await fetch("/api/admin/apikeys");
    var json = await res.json();
    var keys = json.data || [];

    tbody.textContent = "";

    if (keys.length === 0) {
      setTableState(tbody, 5, "No API keys created yet", "empty-cell");
      return;
    }

    keys.forEach(function (k) {
      var tr = document.createElement("tr");
      tr.appendChild(createCell(k.name));
      tr.appendChild(createCodeCell(k.key_prefix || k.prefix));

      var scopes = k.scopes ? k.scopes.join(", ") : "all";
      tr.appendChild(createCell(scopes));

      var natsStatus = k.nats_pub_key ? "ACTIVE" : "---";
      var natsCls = k.nats_pub_key ? "badge-ok" : "";
      if (k.nats_pub_key) {
        tr.appendChild(createBadgeCell(natsStatus, natsCls));
      } else {
        tr.appendChild(createCell(natsStatus));
      }

      var actionTd = document.createElement("td");
      var revokeBtn = document.createElement("button");
      revokeBtn.className = "btn-danger btn-sm";
      revokeBtn.textContent = "Revoke";
      revokeBtn.addEventListener("click", function () {
        revokeAPIKey(k.key_id);
      });
      actionTd.appendChild(revokeBtn);
      tr.appendChild(actionTd);

      tbody.appendChild(tr);
    });
  } catch (err) {
    setTableState(tbody, 5, "Failed to load API keys", "error-cell");
  }
}

async function createAPIKey() {
  var name = document.getElementById("apikey-name").value.trim();
  var scopes = getSelectedScopes();
  if (!name) return;

  var resultEl = document.getElementById("apikey-result");

  try {
    var res = await fetch("/api/admin/apikeys", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ name: name, scopes: scopes }),
    });

    var json = await res.json();
    resultEl.textContent = "";
    resultEl.classList.remove("hidden", "success", "error");

    if (res.ok) {
      var data = json.data || json;
      var key = data.api_key || data.key || "";
      var natsSeed = data.nats_seed || "";

      resultEl.classList.add("success");

      var strong = document.createElement("strong");
      strong.textContent = "API key created! ";
      resultEl.appendChild(strong);
      resultEl.appendChild(
        document.createTextNode("Copy the credentials now (shown once):")
      );
      resultEl.appendChild(document.createElement("br"));

      var label1 = document.createElement("span");
      label1.className = "scope-label";
      label1.textContent = "API Key: ";
      label1.style.marginTop = "8px";
      label1.style.display = "inline-block";
      resultEl.appendChild(label1);

      var code = document.createElement("code");
      code.textContent = key;
      resultEl.appendChild(code);

      var copyBtn = document.createElement("button");
      copyBtn.className = "btn-copy";
      copyBtn.textContent = "Copy";
      copyBtn.addEventListener("click", function () {
        navigator.clipboard.writeText(key);
        copyBtn.textContent = "Copied!";
        setTimeout(function () {
          copyBtn.textContent = "Copy";
        }, 2000);
      });
      resultEl.appendChild(copyBtn);

      if (natsSeed) {
        resultEl.appendChild(document.createElement("br"));
        var label2 = document.createElement("span");
        label2.className = "scope-label";
        label2.textContent = "NATS Seed: ";
        label2.style.marginTop = "6px";
        label2.style.display = "inline-block";
        resultEl.appendChild(label2);

        var seedCode = document.createElement("code");
        seedCode.textContent = natsSeed;
        resultEl.appendChild(seedCode);

        var copySeedBtn = document.createElement("button");
        copySeedBtn.className = "btn-copy";
        copySeedBtn.textContent = "Copy";
        copySeedBtn.addEventListener("click", function () {
          navigator.clipboard.writeText(natsSeed);
          copySeedBtn.textContent = "Copied!";
          setTimeout(function () {
            copySeedBtn.textContent = "Copy";
          }, 2000);
        });
        resultEl.appendChild(copySeedBtn);
      }

      document.getElementById("apikey-name").value = "";
      loadAPIKeys();
    } else {
      resultEl.classList.add("error");
      resultEl.textContent = json.message || "Failed to create API key";
    }
  } catch (err) {
    resultEl.textContent = "";
    resultEl.classList.remove("hidden", "success", "error");
    resultEl.classList.add("error");
    resultEl.textContent = "Error: " + err.message;
  }
}

async function revokeAPIKey(id) {
  if (!confirm("Revoke this API key? This cannot be undone.")) return;

  try {
    await fetch("/api/admin/apikeys/" + encodeURIComponent(id), {
      method: "DELETE",
    });
    loadAPIKeys();
  } catch (err) {
    alert("Failed to revoke key: " + err.message);
  }
}

// ---------------------------------------------------------------------------
// Init
// ---------------------------------------------------------------------------
document.querySelectorAll("[data-admin-tab]").forEach(function (tab) {
  tab.addEventListener("click", function () {
    switchTab(tab.dataset.adminTab);
  });
});

var createInviteBtn = document.getElementById("create-invite-btn");
if (createInviteBtn) createInviteBtn.addEventListener("click", createInvite);

var createAPIKeyBtn = document.getElementById("create-apikey-btn");
if (createAPIKeyBtn) createAPIKeyBtn.addEventListener("click", createAPIKey);

loadUsers();
loadAPIKeys();
initScopeToggles();
