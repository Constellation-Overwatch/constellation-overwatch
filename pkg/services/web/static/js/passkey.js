// passkey.js
// WebAuthn passkey authentication for Constellation Overwatch.
// Implements email-first login and registration flows using the
// Web Authentication API (navigator.credentials).

"use strict";

// ---------------------------------------------------------------------------
// Base64url <-> ArrayBuffer helpers
// ---------------------------------------------------------------------------

function bufferToBase64url(buffer) {
  var bytes = new Uint8Array(buffer);
  var binary = "";
  for (var i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i]);
  }
  return btoa(binary)
    .replace(/\+/g, "-")
    .replace(/\//g, "_")
    .replace(/=+$/, "");
}

function base64urlToBuffer(base64url) {
  var base64 = base64url.replace(/-/g, "+").replace(/_/g, "/");
  var pad = base64.length % 4;
  var padded = pad ? base64 + "=".repeat(4 - pad) : base64;
  var binary = atob(padded);
  var bytes = new Uint8Array(binary.length);
  for (var i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes.buffer;
}

// ---------------------------------------------------------------------------
// Status helpers
// ---------------------------------------------------------------------------

function showStatus(msg, isError) {
  var el = document.getElementById("status-message");
  if (!el) return;
  el.textContent = msg;
  el.className = "login-status";
  if (isError) {
    el.classList.add("error");
  } else {
    el.classList.add("success");
  }
}

function hideStatus() {
  var el = document.getElementById("status-message");
  if (!el) return;
  el.textContent = "";
  el.className = "login-status hidden";
}

// ---------------------------------------------------------------------------
// Email-first passkey login
// ---------------------------------------------------------------------------

async function handleLoginNext(event) {
  event.preventDefault();
  hideStatus();

  var email = document.getElementById("email-input").value.trim();
  if (!email) {
    showStatus("Please enter your email address.", true);
    return false;
  }

  var btn = document.getElementById("login-next-btn");
  btn.disabled = true;
  btn.textContent = "Authenticating...";

  try {
    // Step 1 - Send email to begin login
    var beginRes = await fetch("/auth/passkey/login/begin", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ email: email }),
    });

    if (!beginRes.ok) {
      var errBody = await beginRes.json().catch(function () {
        return { error: "Login failed" };
      });
      showStatus(errBody.error || "Login failed", true);
      btn.disabled = false;
      btn.textContent = "Next";
      return false;
    }

    var response = await beginRes.json();

    // If user needs passkey setup (bootstrap flow), redirect
    if (response.action === "setup") {
      window.location.href = response.redirect || "/overwatch";
      return false;
    }

    // Step 2 - Decode and invoke WebAuthn API
    var publicKeyOptions = {
      challenge: base64urlToBuffer(response.publicKey.challenge),
      timeout: response.publicKey.timeout || 60000,
      rpId: response.publicKey.rpId,
      userVerification: response.publicKey.userVerification || "preferred",
    };

    if (
      response.publicKey.allowCredentials &&
      response.publicKey.allowCredentials.length > 0
    ) {
      publicKeyOptions.allowCredentials =
        response.publicKey.allowCredentials.map(function (cred) {
          return {
            type: cred.type,
            id: base64urlToBuffer(cred.id),
            transports: cred.transports,
          };
        });
    }

    var assertion = await navigator.credentials.get({
      publicKey: publicKeyOptions,
    });

    // Step 3 - Send assertion to finish login
    var payload = {
      id: assertion.id,
      rawId: bufferToBase64url(assertion.rawId),
      type: assertion.type,
      response: {
        authenticatorData: bufferToBase64url(
          assertion.response.authenticatorData
        ),
        clientDataJSON: bufferToBase64url(assertion.response.clientDataJSON),
        signature: bufferToBase64url(assertion.response.signature),
        userHandle: assertion.response.userHandle
          ? bufferToBase64url(assertion.response.userHandle)
          : "",
      },
    };

    var finishRes = await fetch("/auth/passkey/login/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });

    if (finishRes.ok) {
      window.location.href = "/overwatch";
      return false;
    }

    var finishErr = await finishRes.json().catch(function () {
      return { error: "Authentication failed" };
    });
    showStatus(finishErr.error || "Authentication failed", true);
  } catch (err) {
    if (err.name === "NotAllowedError") {
      showStatus("Authentication was cancelled or not allowed.", true);
    } else {
      showStatus("Login error: " + err.message, true);
    }
  }

  btn.disabled = false;
  btn.textContent = "Next";
  return false;
}

// ---------------------------------------------------------------------------
// Passkey registration (requires active session)
// ---------------------------------------------------------------------------

async function registerPasskey() {
  hideStatus();

  try {
    var beginRes = await fetch("/auth/passkey/register/begin", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
    });

    if (!beginRes.ok) {
      var errBody = await beginRes.text();
      showStatus("Registration failed: " + errBody, true);
      return false;
    }

    var options = await beginRes.json();

    var publicKeyOptions = {
      challenge: base64urlToBuffer(options.publicKey.challenge),
      rp: options.publicKey.rp,
      user: {
        id: base64urlToBuffer(options.publicKey.user.id),
        name: options.publicKey.user.name,
        displayName: options.publicKey.user.displayName,
      },
      pubKeyCredParams: options.publicKey.pubKeyCredParams,
      timeout: options.publicKey.timeout || 60000,
      attestation: options.publicKey.attestation || "none",
      authenticatorSelection: options.publicKey.authenticatorSelection || {
        residentKey: "required",
        userVerification: "preferred",
      },
    };

    if (
      options.publicKey.excludeCredentials &&
      options.publicKey.excludeCredentials.length > 0
    ) {
      publicKeyOptions.excludeCredentials =
        options.publicKey.excludeCredentials.map(function (cred) {
          return {
            type: cred.type,
            id: base64urlToBuffer(cred.id),
            transports: cred.transports,
          };
        });
    }

    var credential = await navigator.credentials.create({
      publicKey: publicKeyOptions,
    });

    var attestationPayload = {
      id: credential.id,
      rawId: bufferToBase64url(credential.rawId),
      type: credential.type,
      response: {
        attestationObject: bufferToBase64url(
          credential.response.attestationObject
        ),
        clientDataJSON: bufferToBase64url(credential.response.clientDataJSON),
        transports:
          typeof credential.response.getTransports === "function"
            ? credential.response.getTransports()
            : [],
      },
    };

    var finishRes = await fetch("/auth/passkey/register/finish", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(attestationPayload),
    });

    if (finishRes.ok) {
      showStatus("Passkey registered successfully.", false);
      return finishRes.json().catch(function () {
        return { redirect: "/login" };
      });
    }

    var finishErr = await finishRes.text();
    showStatus("Registration failed: " + finishErr, true);
    return null;
  } catch (err) {
    if (err.name === "NotAllowedError") {
      showStatus("Registration was cancelled or not allowed.", true);
    } else if (err.name === "InvalidStateError") {
      showStatus(
        "A passkey already exists for this account on this device.",
        true
      );
    } else {
      showStatus("Registration error: " + err.message, true);
    }
    return null;
  }
}

// ---------------------------------------------------------------------------
// Setup passkey page handler (first-time passkey registration)
// ---------------------------------------------------------------------------

async function beginSetupPasskey() {
  var btn = document.getElementById("setup-passkey-btn");
  btn.disabled = true;
  btn.textContent = "Registering...";
  hideStatus();

  var result = await registerPasskey();
  if (result) {
    showStatus("Passkey registered! Redirecting...", false);
    window.location.href = result.redirect || "/login";
  } else {
    btn.disabled = false;
    btn.textContent = "Register Passkey";
  }
}
