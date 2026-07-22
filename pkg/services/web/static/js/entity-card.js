// toggleEntityDetail expands an entity card and manages its optional WHEP stream.
// Keeping this logic in a same-origin asset allows a strict nonce-based CSP even
// when entity cards themselves are rendered dynamically over SSE.
function toggleEntityDetail(entityId) {
    const card = document.getElementById('c4-entity-' + entityId);
    if (!card) return;

    const body = card.querySelector('.c4-card-body');
    const isDetailed = card.classList.toggle('detailed');

    if (isDetailed) {
        body.style.display = 'block';
        if (typeof connectWHEP === 'function') connectWHEP(entityId);
    } else {
        body.style.display = 'none';
        if (typeof disconnectWHEP === 'function') disconnectWHEP(entityId);
    }
}

async function copyInteractionValue(button) {
    const value = button.dataset.copyValue || '';
    try {
        await navigator.clipboard.writeText(value);
    } catch (_) {
        const textarea = document.createElement('textarea');
        textarea.value = value;
        textarea.style.position = 'fixed';
        textarea.style.left = '-9999px';
        document.body.appendChild(textarea);
        textarea.select();
        const copied = document.execCommand('copy');
        document.body.removeChild(textarea);
        if (!copied) throw new Error('copy failed');
    }

    const original = button.textContent;
    button.textContent = 'Copied!';
    setTimeout(function () { button.textContent = original; }, 1500);
}

document.addEventListener('click', function (event) {
    const button = event.target.closest('[data-copy-value]');
    if (!button) return;
    copyInteractionValue(button).catch(function () {
        window.alert('Copy failed. Please use HTTPS.');
    });
});
