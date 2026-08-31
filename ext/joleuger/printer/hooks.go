// Package printer implements image printing via the CUPS lp command
// as an extension to seedwright.
package printer

import (
	"context"
	"html/template"
	"strings"

	"seedwright/internal/app"
)

// RegisterHooks appends the printer extension's hook to the app's
// ElementActions hook slice. It renders a print button on the element
// detail page and a modal dialog for selecting the printer.
func (e *Extension) RegisterHooks(a *app.App) {
	if a.Hooks == nil {
		return
	}
	a.Hooks.ElementActions = append(a.Hooks.ElementActions, func(ctx context.Context, project, elementID string) (template.HTML, error) {
		return e.renderPrintButton(project, elementID)
	})
}

// renderPrintButton returns the print button HTML and an inline modal
// dialog for printer selection. The button appears on the element
// detail page (via the ElementActions hook).
func (e *Extension) renderPrintButton(project, elementID string) (template.HTML, error) {
	modal := e.renderModal(project)
	return template.HTML(`
<button class="btn btn-secondary" onclick="window.openPrintModal(` + quote(elementID) + `)" title="Print this image" style="padding:0.4rem 0.8rem;">🖨️ Print</button>

` + modal), nil
}

// renderModal returns the full modal HTML + CSS + JS as a string.
func (e *Extension) renderModal(project string) string {
	return `
<style>
.print-modal {
  display: none;
  position: fixed;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  background: rgba(0,0,0,0.6);
  z-index: 10000;
  justify-content: center;
  align-items: center;
}
.print-modal.visible {
  display: flex;
}
.print-modal-content {
  background: #1a1a2e;
  border: 1px solid #0f3460;
  border-radius: 12px;
  padding: 1.5rem;
  max-width: 420px;
  width: 90%;
  max-height: 80vh;
  overflow-y: auto;
  box-shadow: 0 8px 32px rgba(0,0,0,0.4);
}
.print-modal-content h3 {
  margin: 0 0 1rem 0;
  color: #e2e8f0;
  font-size: 1.1rem;
}
.print-preview img {
  width: 100%;
  max-height: 200px;
  object-fit: contain;
  border-radius: 6px;
  background: #0f3460;
  display: block;
}
.print-options {
  margin-top: 1rem;
}
.print-options label {
  display: block;
  margin-bottom: 0.75rem;
  color: #e2e8f0;
  font-size: 0.9rem;
}
.print-options select,
.print-options input[type="number"] {
  display: block;
  width: 100%;
  margin-top: 0.25rem;
  padding: 0.4rem 0.6rem;
  background: #16213e;
  border: 1px solid #0f3460;
  border-radius: 6px;
  color: #e2e8f0;
  font-size: 0.9rem;
}
.print-modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 0.5rem;
  margin-top: 1rem;
}
.print-modal-footer button {
  padding: 0.4rem 1rem;
  border: none;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.9rem;
}
.btn-print-cancel {
  background: #333;
  color: #e2e8f0;
}
.btn-print-submit {
  background: #e94560;
  color: white;
}
.btn-print-submit:disabled {
  background: #666;
  cursor: not-allowed;
}
.print-status {
  margin-top: 0.75rem;
  padding: 0.5rem;
  border-radius: 6px;
  font-size: 0.85rem;
  display: none;
}
.print-status.success {
  display: block;
  background: #0a3d2e;
  color: #4ade80;
  border: 1px solid #166534;
}
.print-status.error {
  display: block;
  background: #3d0a0a;
  color: #f87171;
  border: 1px solid #991b1b;
}
</style>

<div id="printModal" class="print-modal" onclick="if(event.target===this)window.closePrintModal()">
  <div class="print-modal-content">
    <h3>🖨️ Print Image</h3>
    <div class="print-preview">
      <img id="printPreviewImg" src="" alt="Preview">
    </div>
    <div class="print-options">
      <label>
        Printer:
        <select id="printPrinter" onchange="window.onPrinterChange()">
          <option value="">Loading printers...</option>
        </select>
      </label>
      <label>
        Copies:
        <input type="number" id="printCopies" value="1" min="1" max="99">
      </label>
      <label style="display:flex;align-items:center;gap:0.4rem;cursor:pointer;">
        <input type="checkbox" id="printCropPreview" onchange="window.updatePrintPreview()" style="width:auto;margin:0;">
        Preview crop (what the printer will print)
      </label>
    </div>
    <div id="printStatus" class="print-status"></div>
    <div class="print-modal-footer">
      <button class="btn-print-cancel" onclick="window.closePrintModal()">Cancel</button>
      <button class="btn-print-submit" id="printSubmit" onclick="window.submitPrint()">Print</button>
    </div>
  </div>
</div>

<script>
(function() {
  var currentElementId = '';
  var printers = [];
  var previewObjURL = null;
  var project = '` + project + `';

  function rawPreviewURL() {
    return url('/basic/' + encodeURIComponent(project) + '/element/' + encodeURIComponent(currentElementId) + '/image');
  }

  // Show the raw element image (replaces any crop preview object URL).
  function loadRawPreview() {
    if (previewObjURL) {
      URL.revokeObjectURL(previewObjURL);
      previewObjURL = null;
    }
    document.getElementById('printPreviewImg').src = rawPreviewURL() + '?t=' + Date.now();
  }

  // The currently selected printer entry, or null.
  function currentPrinter() {
    var sel = document.getElementById('printPrinter');
    if (!sel.value) return null;
    for (var i = 0; i < printers.length; i++) {
      if ((printers[i].uri || printers[i].name) === sel.value) return printers[i];
    }
    return null;
  }

  // The crop toggle is on by default for crop printers, disabled for
  // raw ones (there is nothing to preview).
  window.syncCropToggle = function() {
    var chk = document.getElementById('printCropPreview');
    if (!chk) return;
    var p = currentPrinter();
    chk.checked = !!(p && p.crop);
    chk.disabled = !(p && p.crop);
  };

  // Show what the printer will print: for a selected crop printer, fetch
  // the processed image from the imageproc preview endpoint; otherwise —
  // or on any error, silently — show the raw element image. A preview
  // failure never blocks printing.
  window.updatePrintPreview = async function() {
    var chk = document.getElementById('printCropPreview');
    var p = currentPrinter();
    if (!(chk && chk.checked && p && p.crop)) {
      loadRawPreview();
      return;
    }
    var dims = (p.dimensions || '1800x1200').split('x');
    try {
      var resp = await fetch(url('/api/' + encodeURIComponent(project) + '/ext/joleuger/imageproc/preview'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json', 'Accept': 'application/json' },
        body: JSON.stringify({
          element_id: currentElementId,
          width: parseInt(dims[0], 10),
          height: parseInt(dims[1], 10),
          fit: 'crop',
          rotate: 'auto'
        })
      });
      if (!resp.ok) throw new Error('HTTP ' + resp.status);
      var blob = await resp.blob();
      if (previewObjURL) URL.revokeObjectURL(previewObjURL);
      previewObjURL = URL.createObjectURL(blob);
      document.getElementById('printPreviewImg').src = previewObjURL;
    } catch (_) {
      loadRawPreview();
    }
  };

  window.openPrintModal = function(elementId) {
    currentElementId = elementId;
    var modal = document.getElementById('printModal');
    modal.classList.add('visible');
    modal.style.display = 'flex';

    // Raw preview first; the crop preview (if enabled) replaces it once
    // the printer list has loaded.
    loadRawPreview();

    // Load printers and check for recent printer.
    var storageKey = 'sdcpp_printer_' + elementId;
    var recentPrinter = localStorage.getItem(storageKey);

    // Fetch configured printers only (?configured=true — the server
    // skips lpstat discovery, so nothing else reaches the dialog).
    var xhr = new XMLHttpRequest();
    xhr.open('GET', url('/api/' + encodeURIComponent(project) + '/ext/joleuger/printer/printers?configured=true'), true);
    xhr.onreadystatechange = function() {
      if (xhr.readyState !== 4) return;
      if (xhr.status !== 200) {
        document.getElementById('printPrinter').innerHTML = '<option value="">Failed to load printers</option>';
        return;
      }
      try {
        var resp = JSON.parse(xhr.responseText);
        printers = resp.printers || [];
        buildPrinterSelect(recentPrinter);
        // Refresh the preview for the selected printer (crop or raw).
        window.syncCropToggle();
        window.updatePrintPreview();
      } catch(e) {
        document.getElementById('printPrinter').innerHTML = '<option value="">Error loading printers</option>';
      }
    };
    xhr.send();
  };

  function buildPrinterSelect(recentPrinter) {
    var sel = document.getElementById('printPrinter');
    sel.innerHTML = '';

    if (printers.length === 0) {
      var none = document.createElement('option');
      none.value = '';
      none.textContent = 'No printers configured — see extensions.joleuger/printer.printers';
      sel.appendChild(none);
      return;
    }

    printers.forEach(function(p) {
      var opt = document.createElement('option');
      opt.value = p.uri || p.name;
      var label = p.name;
      if (p.status && p.status !== 'unknown') label += ' (' + p.status + ')';
      opt.textContent = label;
      opt.setAttribute('data-uri', p.uri || p.name);
      sel.appendChild(opt);
    });

    // Auto-select recent printer if available.
    if (recentPrinter) {
      for (var i = 0; i < sel.options.length; i++) {
        if (sel.options[i].getAttribute('data-uri') === recentPrinter) {
          sel.selectedIndex = i;
          return;
        }
      }
    }
  }

  window.closePrintModal = function() {
    var modal = document.getElementById('printModal');
    modal.classList.remove('visible');
    modal.style.display = 'none';
    if (previewObjURL) {
      URL.revokeObjectURL(previewObjURL);
      previewObjURL = null;
    }
    var status = document.getElementById('printStatus');
    status.className = 'print-status';
    status.textContent = '';
    document.getElementById('printSubmit').disabled = false;
    document.getElementById('printSubmit').textContent = 'Print';
  };

  window.onPrinterChange = function() {
    window.syncCropToggle();
    window.updatePrintPreview();
  };

  window.submitPrint = function() {
    var sel = document.getElementById('printPrinter');
    if (!sel.value) {
      showStatus('Please select a printer.', 'error');
      return;
    }
    var uri = sel.options[sel.selectedIndex].getAttribute('data-uri');
    var copies = parseInt(document.getElementById('printCopies').value, 10) || 1;
    if (copies < 1) copies = 1;
    if (copies > 99) copies = 99;

    var submitBtn = document.getElementById('printSubmit');
    submitBtn.disabled = true;
    submitBtn.textContent = 'Printing...';
    showStatus('', '');

    var body = JSON.stringify({
      element_id: currentElementId,
      printer_uri: uri,
      copies: copies
    });

    var xhr = new XMLHttpRequest();
    xhr.open('POST', url('/api/' + encodeURIComponent(project) + '/ext/joleuger/printer/print'), true);
    xhr.setRequestHeader('Content-Type', 'application/json');
    xhr.onreadystatechange = function() {
      if (xhr.readyState !== 4) return;
      if (xhr.status === 200) {
        try {
          var resp = JSON.parse(xhr.responseText);
          // Save recent printer to localStorage.
          localStorage.setItem('sdcpp_printer_' + currentElementId, uri);
          showStatus('Printed! Job ID: ' + resp.job_id, 'success');
          submitBtn.textContent = 'Done';
          submitBtn.disabled = false;
        } catch(e) {
          showStatus('Print submitted.', 'success');
          submitBtn.textContent = 'Done';
          submitBtn.disabled = false;
        }
      } else {
        var msg = 'Print failed';
        try {
          var err = JSON.parse(xhr.responseText);
          if (err.error) msg = err.error;
        } catch(e2) {
          msg = 'HTTP ' + xhr.status;
        }
        showStatus(msg, 'error');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Retry';
      }
    };
    xhr.send(body);
  };

  function showStatus(msg, cls) {
    var el = document.getElementById('printStatus');
    el.textContent = msg;
    el.className = 'print-status' + (cls ? ' ' + cls : '');
  }
})();
</script>`
}

// quote wraps a string in single quotes for inline JS.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "\\'") + "'"
}
