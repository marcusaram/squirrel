'use strict';

const $ = (s, el = document) => el.querySelector(s);
const token = new URLSearchParams(location.search).get('t') || '';
const form = $('#settings');

// In the Mac app the page runs in a web view without confirm() or
// alert(); the app's own dialogs answer instead.
let NATIVE = false;
let PLATFORM = '';

async function ask(message, ok) {
  if (!NATIVE) return confirm(message);
  const r = await api('ask', { method: 'POST', body: JSON.stringify({ message, ok: ok || t('Continue') }) });
  return !!(r && r.ok);
}

async function tell(message) {
  if (!NATIVE) { alert(message); return; }
  await api('tell', { method: 'POST', body: JSON.stringify({ message }) }).catch(() => {});
}

async function api(path, opts = {}) {
  opts.headers = Object.assign({ 'X-Token': token, 'Content-Type': 'application/json' }, opts.headers || {});
  const r = await fetch('api/' + path, opts);
  const data = await r.json().catch(() => null);
  if (!r.ok) throw new Error((data && data.error) || r.statusText);
  return data;
}

const gb = n => (n / 1e9).toLocaleString(LANG === 'nl' ? 'nl-NL' : 'en-US', { maximumFractionDigits: 0 }) + ' GB';

// ---------- form <-> settings ----------

function wifiRow(net = { ssid: '', password: '' }) {
  const row = document.createElement('div');
  row.className = 'net';
  row.innerHTML = `
    <label class="field">${t('Network name')}<input class="ssid" autocomplete="off"></label>
    <label class="field">${t('Password')}<span class="pwbox"><input class="pw" type="password" autocomplete="new-password"><button type="button" class="link show">${t('Show')}</button></span></label>
    <button type="button" class="link remove">${t('Remove')}</button>`;
  $('.ssid', row).value = net.ssid || '';
  $('.pw', row).value = net.password || '';
  // Passwords are never remembered; say so where one was used before.
  if (net.ssid && !net.password) $('.pw', row).placeholder = t('enter the password again');
  $('.remove', row).onclick = () => {
    row.remove();
    updateRemove();
  };
  $('#wifi').append(row);
  updateRemove();
}

// The last network cannot be removed; there is always a row to fill in.
function updateRemove() {
  const rows = document.querySelectorAll('.net');
  rows.forEach(r => { $('.remove', r).hidden = rows.length < 2; });
}

let boards = [], appVersion = '', payloadOK = true;
const boardOf = id => boards.find(b => b.id === id) || boards[0] || {};

// The OS images for the chosen board: the tested one first, then newer
// releases from the publisher, marked as untested.
let releases = [], releasesFor = '', releasesError = '', savedImage = '';

function renderReleases() {
  const box = $('#releases');
  box.replaceChildren();
  const want = releases.some(r => !r.tested && r.URL === savedImage) ? savedImage : '';
  for (const r of releases) {
    const l = document.createElement('label');
    l.className = 'disk';
    l.innerHTML = '<input type="radio" name="image"><span class="name"></span><span class="size"></span>';
    const input = $('input', l);
    input.value = r.tested ? '' : r.URL;
    input.checked = input.value === want;
    $('.name', l).textContent = r.Name;
    $('.size', l).textContent = r.tested ? t('Tested with Squirrel') : t('Not tested with Squirrel yet');
    box.append(l);
  }
}

async function loadReleases(board) {
  releasesFor = board.id;
  releases = [{ ...board.image, tested: true }];
  releasesError = '';
  renderReleases();
  try {
    const d = await api('releases?board=' + encodeURIComponent(board.id));
    if (releasesFor !== board.id) return;
    releases = d.releases;
    releasesError = d.error ? t('Newer releases could not be listed (no internet?); the tested one is offered.') : '';
  } catch (err) {
    releasesError = t('Newer releases could not be listed (no internet?); the tested one is offered.');
  }
  renderReleases();
  sync();
}

function fill(s) {
  for (const el of form.elements) {
    if (!el.name) continue;
    const [a, b] = el.name.split('.');
    const v = b ? (s[a] || {})[b] : s[a];
    if (el.type === 'checkbox') el.checked = !!v;
    else if (el.type === 'radio') el.checked = el.value === (v || (a === 'board' ? 'rpi' : ''));
    else if (v != null) el.value = v;
  }
  // Where the computer could not tell the time zone by its IANA name
  // (Windows), the browser can.
  if (!form.elements.timezone.value) {
    try { form.elements.timezone.value = Intl.DateTimeFormat().resolvedOptions().timeZone || ''; } catch {}
  }
  form.elements.usb_storage.checked = s.storage === 'usb';
  form.elements.sd_overclock.checked = !!s.sd_overclock;
  savedImage = s.image || '';
  $('#wifi').replaceChildren();
  (s.wifi && s.wifi.length ? s.wifi : [{}]).forEach(wifiRow);
  sync();
}

function read() {
  const s = { nas: { upload: { saved: true, sentry: true, track: true } } };
  for (const el of form.elements) {
    if (!el.name || el.type === 'radio' && !el.checked) continue;
    const [a, b] = el.name.split('.');
    const v = el.type === 'checkbox' ? el.checked : el.type === 'range' ? +el.value : el.value.trim();
    if (b) (s[a] = s[a] || {})[b] = v;
    else s[a] = v;
  }
  s.wifi = [...document.querySelectorAll('.net')]
    .map(r => ({ ssid: $('.ssid', r).value.trim(), password: $('.pw', r).value }))
    .filter(n => n.ssid);
  s.storage = s.usb_storage && boardOf(s.board).usb_storage ? 'usb' : '';
  delete s.usb_storage;
  s.sd_overclock = !!s.sd_overclock && ['rpi', 'rpizerow'].includes(s.board);
  return s;
}

function sync() {
  $('#hostpreview').textContent = form.elements.hostname.value || 'squirrel';
  $('#campct').textContent = form.elements.cam_percent.value + '%';
  $('#nasfields').hidden = !form.elements['nas.enabled'].checked;
  const board = boardOf(form.elements.board.value);
  $('#usbrow').hidden = !board.usb_storage;
  $('#overclockrow').hidden = !['rpi', 'rpizerow'].includes(board.id);
  if (board.id) {
    if (board.id !== releasesFor) loadReleases(board);
    const checked = form.querySelector('input[name=image]:checked');
    if (checked) savedImage = checked.value;
    const rel = releases.find(r => (r.tested ? '' : r.URL) === (checked ? checked.value : '')) || releases[0];
    $('#osnote').textContent = rel.Size
      ? t('This goes on the card. {0} ({1} MB) is downloaded once.', rel.Name, Math.round(rel.Size / 1e6))
      : t('This goes on the card. {0} is downloaded once.', rel.Name);
    const note = $('#releasenote');
    note.textContent = rel.tested ? releasesError
      : t('Not tested with Squirrel yet. It will probably work, but a change in the OS can break the first start. The tested image is the safe choice.');
    note.hidden = !note.textContent;
    $('#imageinfo').textContent = t('Base: {0}. squirrel-maker {1}.', rel.Name, appVersion) +
      (payloadOK ? '' : t(' Note: this version was built without squirreld; use make maker.'));
  }
}
form.addEventListener('input', sync);
$('#addwifi').onclick = () => {
  wifiRow();
  $('.net:last-child .ssid').focus();
};

// "Show" next to a password field.
document.addEventListener('click', e => {
  const b = e.target.closest('.show');
  if (!b) return;
  const input = b.parentElement.querySelector('input');
  const hidden = input.type === 'password';
  input.type = hidden ? 'text' : 'password';
  b.textContent = hidden ? t('Hide') : t('Show');
});

// ---------- jobs ----------

function showJob(box, j) {
  box.hidden = false;
  const pct = j.frac >= 0 ? Math.round(j.frac * 100) + '%' : '';
  box.innerHTML = `<div class="label"><span></span><span class="num"></span></div><div class="track"><i></i></div>`;
  $('.label span', box).textContent = tErr(j.step);
  $('.num', box).textContent = j.done ? '' : pct;
  const bar = $('.track', box);
  bar.classList.toggle('busy', !j.done && !(j.frac >= 0));
  $('i', bar).style.width = j.done && !j.error ? '100%' : j.frac >= 0 ? j.frac * 100 + '%' : '';
  if (j.error) {
    const e = document.createElement('p');
    e.className = 'error';
    e.textContent = tErr(j.error);
    box.append(e);
  }
}

async function follow(box, onDone) {
  for (;;) {
    const j = await api('job').catch(() => null);
    if (j) showJob(box, j);
    if (!j || j.done) return onDone(j);
    await new Promise(r => setTimeout(r, 500));
  }
}

form.addEventListener('submit', async e => {
  e.preventDefault();
  // Enter in a field means "next", not "build the image".
  if (cur !== BUILD) { next(); return; }
  const box = $('#buildjob');
  const btn = $('#build');
  btn.disabled = true;
  try {
    await api('build', { method: 'POST', body: JSON.stringify(read()) });
    await follow(box, j => {
      btn.disabled = false;
      if (j && j.done && !j.error) {
        const p = document.createElement('p');
        p.className = 'result';
        p.innerHTML = `${t('Image ready.')} <button type="button" class="link">${t(PLATFORM === 'windows' ? 'Show in Explorer' : 'Show in Finder')}</button>`;
        $('button', p).onclick = () => api('reveal', { method: 'POST' });
        box.append(p);
        built = true;
        updateWrite();
        loadDisks();
        show(WRITE);
      }
    });
  } catch (err) {
    btn.disabled = false;
    showJob(box, { step: 'Not started', frac: 0, done: true, error: err.message });
  }
});

// ---------- cards ----------

let built = false;

function updateWrite() {
  $('#write').disabled = !built || !document.querySelector('input[name=disk]:checked');
}

async function loadDisks() {
  const box = $('#disks');
  try {
    const disks = await api('disks');
    if (!disks.length) {
      box.innerHTML = `<p class="empty">${t('No SD card found. Insert one and choose Search again.')}</p>`;
    } else {
      box.replaceChildren(...disks.map(d => {
        const l = document.createElement('label');
        l.className = 'disk';
        l.innerHTML = '<input type="radio" name="disk"><span class="name"></span><span class="size"></span>';
        $('input', l).value = d.id;
        $('.name', l).textContent = `${d.name || t('Card')} (${d.id})`;
        $('.size', l).textContent = gb(d.size);
        $('input', l).onchange = updateWrite;
        l.dataset.label = t('{0} of {1}', d.name || t('card'), gb(d.size));
        return l;
      }));
    }
  } catch (err) {
    box.innerHTML = '';
    const p = document.createElement('p');
    p.className = 'empty';
    p.textContent = t('Looking for cards failed: {0}', tErr(err.message));
    box.append(p);
  }
  updateWrite();
}
$('#refresh').onclick = loadDisks;

$('#write').onclick = async () => {
  const sel = document.querySelector('input[name=disk]:checked');
  if (!sel) return;
  const what = sel.closest('.disk').dataset.label;
  if (!await ask(t('Everything on the {0} ({1}) is erased and replaced by the Squirrel image. Continue?', what, sel.value), t('Erase and write'))) return;
  const box = $('#writejob');
  $('#write').disabled = true;
  try {
    await api('write', { method: 'POST', body: JSON.stringify({ disk: sel.value }) });
    await follow(box, j => {
      updateWrite();
      if (j && j.done && !j.error) {
        $('#url').textContent = `http://${form.elements.hostname.value || 'squirrel'}.local/`;
        written = true;
        show(DONE);
      }
    });
  } catch (err) {
    updateWrite();
    showJob(box, { step: 'Not started', frac: 0, done: true, error: err.message });
  }
};

// ---------- updating a card ----------

const BOARD_NAME = { rpi: 'Raspberry Pi', rpizerow: 'Raspberry Pi Zero W', rock4cplus: 'Radxa ROCK 4C+' };

// The wizard and the updater take turns on the page.
function showUpdater(on) {
  $('#updater').hidden = !on;
  form.hidden = $('#stepper').hidden = on;
  if (on) {
    for (const id of ['writestep', 'after', 'wiznav']) $('#' + id).hidden = true;
    loadCards();
  } else {
    $('#writestep').hidden = $('#after').hidden = false;
    show(cur);
  }
  window.scrollTo({ top: 0 });
}
$('#toupdate').onclick = () => showUpdater(true);
$('#tonew').onclick = () => showUpdater(false);

function updateUpdate() {
  $('#doupdate').disabled = !document.querySelector('input[name=udisk]:checked');
}

async function loadCards() {
  const box = $('#udisks');
  box.innerHTML = `<p class="empty">${t('Looking for cards…')}</p>`;
  let disks;
  try {
    disks = await api('disks');
  } catch (err) {
    box.innerHTML = '';
    const p = document.createElement('p');
    p.className = 'empty';
    p.textContent = t('Looking for cards failed: {0}', tErr(err.message));
    box.append(p);
    return;
  }
  if (!disks.length) {
    box.innerHTML = `<p class="empty">${t('No SD card found. Insert one and choose Search again.')}</p>`;
    updateUpdate();
    return;
  }
  const rows = await Promise.all(disks.map(async d => {
    const l = document.createElement('label');
    l.className = 'disk';
    l.innerHTML = '<input type="radio" name="udisk"><span class="name"></span><span class="size"></span>';
    const input = $('input', l);
    input.value = d.id;
    input.onchange = updateUpdate;
    $('.name', l).textContent = `${d.name || t('Card')} (${d.id})`;
    try {
      const c = await api('card?id=' + encodeURIComponent(d.id));
      const board = BOARD_NAME[c.board] || c.board;
      $('.size', l).textContent = c.board === 'rock4cplus' && !c.version
        ? t('{0}; the version is read when updating', board)
        : t('Squirrel {0} for {1}', c.version || t('(older version)'), board) + (c.pending ? ', ' + t('update not installed yet') : '');
      l.dataset.label = t('{0} of {1}', d.name || t('card'), gb(d.size));
    } catch (err) {
      input.disabled = true;
      l.classList.add('off');
      $('.size', l).textContent = tErr(err.message);
    }
    return l;
  }));
  box.replaceChildren(...rows);
  updateUpdate();
}
$('#urefresh').onclick = loadCards;

$('#doupdate').onclick = async () => {
  const sel = document.querySelector('input[name=udisk]:checked');
  if (!sel) return;
  if (!await ask(t('Squirrel {0} is put on the {1}. Settings and clips stay. Continue?', appVersion, sel.closest('.disk').dataset.label), t('Update card'))) return;
  const box = $('#updatejob');
  $('#doupdate').disabled = true;
  try {
    await api('updatecard', { method: 'POST', body: JSON.stringify({ disk: sel.value }) });
    await follow(box, j => {
      updateUpdate();
      if (j && j.done && !j.error) {
        const p = document.createElement('p');
        p.className = 'result';
        p.textContent = t('Put the card back in the Pi and start it. The LED blinks fast for a minute while the update is installed.');
        box.append(p);
      }
    });
  } catch (err) {
    updateUpdate();
    showJob(box, { step: 'Not started', frac: 0, done: true, error: err.message });
  }
};

// ---------- wizard ----------

const steps = [...document.querySelectorAll('section.step')];
const [BOARD, WIFI, LOGIN, NAS, BUILD, WRITE, DONE] = steps.keys();
let cur = 0, reached = 0, written = false;

// check returns why the current step cannot be left yet, or ''.
async function check(i) {
  const el = form.elements;
  const s = read();
  switch (i) {
    case WIFI:
      if (!s.wifi.length) return t('Enter at least one wifi network.');
      if (!/^[A-Za-z]{2}$/.test(s.country)) return t('The country is a two-letter code, for example NL.');
      if (s.ap_password.length < 8 || s.ap_password.length > 63) return t('The hotspot password must be 8 to 63 characters.');
      for (const n of s.wifi) {
        if (n.password && (n.password.length < 8 || n.password.length > 63)) return t('The password for {0} must be 8 to 63 characters.', n.ssid);
      }
      {
        const open = s.wifi.filter(n => !n.password).map(n => n.ssid);
        if (open.length && !await ask(t('No password is filled in for {0}. The Pi treats that as an open network without a password. Is that right?', open.join(', ')))) return t('Enter the password.');
      }
      return '';
    case LOGIN:
      if (!/^[a-z0-9]([a-z0-9-]*[a-z0-9])?$/.test(s.hostname)) return t("The Pi's name may only contain lowercase letters, digits and hyphens.");
      if (!/^[a-z_][a-z0-9_-]*$/.test(s.user) || s.user === 'root') return t('Choose a user name with lowercase letters and digits (not root).');
      if (!s.password && !s.ssh_key) return t('Enter a password or an SSH key, or you cannot log in to the Pi.');
      if (s.password && s.password.length < 8) return t('The password must be at least 8 characters.');
      return '';
    case NAS:
      if (el['nas.enabled'].checked && (!s.nas.host || !s.nas.share)) return t("Enter the NAS's server and shared folder, or turn uploading off.");
      return '';
    case BUILD:
      return built ? '' : t('Build the image first.');
    case WRITE:
      return written ? '' : t('Write the image to a card first.');
  }
  return '';
}

function renderStepper() {
  $('#stepper').style.setProperty('--n', steps.length);
  $('#stepper').replaceChildren(...steps.map((sec, i) => {
    const li = document.createElement('li');
    if (i === cur) li.setAttribute('aria-current', 'step');
    if (i !== cur && (i < cur || i < reached)) li.classList.add('done');
    const b = document.createElement('button');
    b.type = 'button';
    b.disabled = i > reached || i === cur;
    b.innerHTML = `<span class="stop">${i + 1}</span><span class="label"></span>`;
    b.setAttribute('aria-label', t('Step {0}: {1}', i + 1, t(sec.dataset.name)));
    b.querySelector('.label').textContent = t(sec.dataset.name);
    b.onclick = () => show(i);
    li.append(b);
    return li;
  }));
}

function show(i) {
  cur = i;
  reached = Math.max(reached, i);
  steps.forEach((sec, k) => sec.classList.toggle('wiz-hidden', k !== i));
  $('#stepnote').textContent = '';
  $('#prev').hidden = i === 0;
  // Building and writing have their own button and move on by themselves.
  $('#next').hidden = i === BUILD || i === WRITE;
  $('#wiznav').hidden = i === DONE;
  if (i === BUILD) renderSummary();
  renderStepper();
  if (i === WRITE) loadDisks();
  window.scrollTo({ top: 0 });
  const first = steps[i].querySelector('input, textarea, button');
  if (first && i !== DONE) first.focus({ preventScroll: true });
}

function renderSummary() {
  const s = read();
  const rows = [
    [t('Board'), { rock4cplus: 'Radxa ROCK 4C+', rpizerow: 'Raspberry Pi Zero W' }[s.board] || 'Raspberry Pi'],
    [t('Wifi'), s.wifi.map(n => n.ssid + (n.password ? '' : ` (${t('open network')})`)).join(', ')],
    [t('Country'), s.country.toUpperCase()],
    [t('The Pi'), t('{0}.local, user {1}', s.hostname, s.user)],
    [t('Log in with'), [s.password && t('password'), s.ssh_key && t('SSH key')].filter(Boolean).join(t(' and '))],
    [t('NAS'), s.nas.enabled ? `${s.nas.host}/${s.nas.share}${s.nas.path ? '/' + s.nas.path : ''}` : t('no upload')],
    [t('For the car'), s.storage === 'usb' ? t('{0}% of the USB drive', s.cam_percent) : t('{0}% of the card', s.cam_percent)],
    [t('Operating system'), (() => {
      const r = releases.find(x => (x.tested ? '' : x.URL) === (s.image || '')) || releases[0] || {};
      return (r.Name || '') + ' (' + (r.tested ? t('Tested with Squirrel') : t('Not tested with Squirrel yet')) + ')';
    })()],
    s.sd_overclock && [t('SD card'), t('Up to 100 MHz (beyond the standard)')],
    [t('Recordings kept on'), s.storage === 'usb' ? t('USB drive (formatted if empty)') : t('the card')],
  ];
  $('#summary').replaceChildren(...rows.filter(Boolean).flatMap(([k, v]) => {
    const dt = document.createElement('dt');
    const dd = document.createElement('dd');
    dt.textContent = k;
    dd.textContent = v;
    return [dt, dd];
  }));
}

$('#another').onclick = () => {
  built = written = false;
  $('#buildjob').hidden = $('#writejob').hidden = true;
  reached = 0;
  show(BOARD);
};

async function next() {
  const why = await check(cur);
  if (why) {
    $('#stepnote').textContent = why;
    return;
  }
  show(cur + 1);
}

$('#next').onclick = next;
$('#prev').onclick = () => show(cur - 1);

// ---------- start ----------

$('#quit').onclick = async () => {
  try {
    await api('quit', { method: 'POST' });
    document.body.innerHTML = `<main><h1>${t('Squirrel Maker has quit')}</h1><p class="muted">${t('You can close this tab.')}</p></main>`;
  } catch (err) { tell(tErr(err.message)); }
};

translatePage();

(async () => {
  try {
    const d = await api('defaults');
    // The Mac app quits with its menu (Cmd-Q), not from the page.
    NATIVE = !!d.native;
    PLATFORM = d.platform || '';
    $('#quit').hidden = NATIVE;
    boards = d.boards || [];
    appVersion = d.version;
    payloadOK = d.payload_ok;
    fill(d.settings);
  } catch (err) {
    document.body.innerHTML = `<main><h1>${t('No access')}</h1><p>${t('Open the link that squirrel-maker shows in the terminal.')}</p></main>`;
    return;
  }
  show(BOARD);
})();
