'use strict';

// ---------- helpers ----------

const $ = (s, el = document) => el.querySelector(s);

function h(tag, props, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function svg(markup) {
  const t = document.createElement('template');
  t.innerHTML = markup.trim();
  return t.content.firstChild;
}

async function api(path, opts = {}) {
  const r = await fetch('api/' + path, opts);
  const data = await r.json().catch(() => ({}));
  if (!r.ok) throw Object.assign(new Error(data.error || r.statusText), { data });
  return data;
}

const mediaURL = rel => 'media/' + rel.split('/').map(encodeURIComponent).join('/');

const nf = new Intl.NumberFormat(LOCALE, { maximumFractionDigits: 1 });
function bytes(n) {
  if (!n) return '0 B';
  const u = ['B', 'kB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return nf.format(n) + ' ' + u[i];
}
const rate = bps => bytes(bps) + '/s';
const count = (n, one, many) => `${n || 0} ${t(n === 1 ? one : many)}`;

// Clip times are the car's local wall clock without a zone; show them as is.
function naive(s) {
  const m = /^(\d{4})-(\d\d)-(\d\d)T(\d\d):(\d\d):(\d\d)/.exec(s || '');
  return m ? new Date(+m[1], m[2] - 1, +m[3], +m[4], +m[5], +m[6]) : null;
}
const hhmm = d => d.toLocaleTimeString(LOCALE, { hour: '2-digit', minute: '2-digit' });
const hhmmss = d => d.toLocaleTimeString(LOCALE, { hour: '2-digit', minute: '2-digit', second: '2-digit' });

function dayLabel(d) {
  const today = new Date(); today.setHours(0, 0, 0, 0);
  const that = new Date(d); that.setHours(0, 0, 0, 0);
  const diff = Math.round((today - that) / 864e5);
  if (diff === 0) return t('today');
  if (diff === 1) return t('yesterday');
  const opts = { weekday: 'long', day: 'numeric', month: 'long' };
  if (that.getFullYear() !== today.getFullYear()) opts.year = 'numeric';
  return d.toLocaleDateString(LOCALE, opts);
}

function duration(sec) {
  sec = Math.round(sec);
  if (sec < 60) return sec + ' s';
  const m = Math.round(sec / 60);
  if (m < 60) return m + ' min';
  return t('{0} h {1} min', Math.floor(m / 60), m % 60);
}

function ago(iso) {
  if (!iso) return t('not yet');
  const s = (Date.now() - new Date(iso)) / 1000;
  if (s < 5) return t('just now');
  if (s < 60) return t('{0} s ago', Math.round(s));
  if (s < 3600) return t('{0} min ago', Math.round(s / 60));
  if (s < 86400) return t('{0} h ago', Math.round(s / 3600));
  return new Date(iso).toLocaleString(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit' });
}

function clipTitle(g) {
  const r = (g.event && g.event.reason) || '';
  if (g.kind === 'recent') return t('Drive');
  if (g.kind === 'track') return t('Track mode');
  if (r.startsWith('sentry_aware_object')) return t('Movement near the car');
  if (r.startsWith('sentry_aware_accel')) return t('Touch or bump');
  if (r.includes('honk')) return t('Saved with the horn');
  if (r.includes('dashcam')) return t('Saved from the dashcam');
  if (r.includes('voice')) return t('Saved by voice');
  if (r) return r.replace(/_/g, ' ');
  return g.kind === 'sentry' ? 'Sentry' : t('Saved clip');
}

function clipSpan(g) {
  const a = naive(g.start), b = naive(g.end);
  return a && b ? (b - a) / 1000 : 0;
}

const CAM_LABEL = {
  front: t('Front'), back: t('Rear'), left_repeater: t('Left'), right_repeater: t('Right'),
  left_pillar: t('Left pillar'), right_pillar: t('Right pillar'),
};
// Speed in km/h, or mph where people drive by it: only with American or
// British English and a time zone there, since many browsers say en-US
// wherever they are. A click on the unit switches, and is remembered.
const UNITS = { kmh: { factor: 3.6, label: 'km/h' }, mph: { factor: 2.23694, label: 'mph' } };
function speedUnit() {
  try { const u = localStorage.getItem('speedUnit'); if (UNITS[u]) return UNITS[u]; } catch { /* only for this visit */ }
  const tz = Intl.DateTimeFormat().resolvedOptions().timeZone || '';
  const mph = (/^en-US$/.test(navigator.language) && /^America\/|^US\/|^Pacific\/Honolulu$/.test(tz))
    || (/^en-GB$/.test(navigator.language) && /^Europe\/London$/.test(tz));
  return mph ? UNITS.mph : UNITS.kmh;
}
let SPEED_UNIT = speedUnit();

// Where each camera sits on the car, in the car drawing's coordinates.
const CAM_DOT = {
  front: [50, 44], back: [50, 186], left_repeater: [13, 66], right_repeater: [87, 66],
  left_pillar: [17, 104], right_pillar: [83, 104],
};

const ICON = {
  play: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 4.5v15l13-7.5z"/></svg>',
  pause: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M6 4h4.5v16H6zM13.5 4H18v16h-4.5z"/></svg>',
  back10: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M11 6V2L5 7l6 5V8a6 6 0 1 1-6 6H3a8 8 0 1 0 8-8z"/></svg>',
  fwd10: '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M13 6V2l6 5-6 5V8a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z"/></svg>',
  drive: '<svg viewBox="0 0 40 30" aria-hidden="true"><path d="M14 2 6 28M26 2l8 26" stroke="currentColor" stroke-width="2" fill="none"/><path d="M20 4v4M20 13v4M20 22v4" stroke="currentColor" stroke-width="2"/></svg>',
};

// ---------- state ----------

const S = {
  status: null,
  groups: [],
  groupsKey: '',
  loaded: false,
  filter: (() => { try { return localStorage.getItem('filter') || 'all'; } catch { return 'all'; } })(),
  player: null,
  view: '',
};

const app = () => $('#app');

// ---------- header ----------

function usbState(st) {
  const g = st.gadget || {};
  const writing = st.activity && st.activity.last_write && Date.now() - new Date(st.activity.last_write) < 5000;
  if (!g.bound) return { cls: 'bad', text: t('USB off') };
  if (g.state === 'configured' && writing) return { cls: 'live', text: t('Car is recording'), extra: rate(st.activity.write_bps) };
  if (g.state === 'configured') return { cls: 'ok', text: t('Connected to the car') };
  if (g.state === 'suspended') return { cls: 'warn', text: t('Car is asleep') };
  return { cls: 'warn', text: t('Waiting for the car') };
}

function nasState(n) {
  switch (n && n.state) {
    case 'uploading': return { cls: 'live', text: t('NAS: {0} to go', count(n.pending, 'file', 'files')) };
    case 'idle': return n.pending ? { cls: 'warn', text: t('NAS: {0} queued', n.pending) } : { cls: 'ok', text: t('NAS up to date') };
    case 'unreachable': return { cls: 'warn', text: t('NAS unreachable') };
    case 'error': return { cls: 'bad', text: t('NAS error') };
    case 'disabled': return { cls: '', text: t('NAS off') };
  }
  return { cls: '', text: 'NAS' };
}

function renderPulse() {
  const p = $('#pulse');
  const st = S.status;
  if (!st) { p.replaceChildren(h('span', {}, h('i', { class: 'dot bad' }), t('No connection to the Pi'))); return; }
  const u = usbState(st), n = nasState(st.nas);
  p.replaceChildren(
    h('span', {}, h('i', { class: 'dot ' + u.cls }), u.text, u.extra ? h('span', { class: 'num' }, u.extra) : null),
    h('span', { class: 'optional' }, t('Last scan {0}', ago(st.preserve.last_scan))),
    h('span', {}, h('i', { class: 'dot ' + n.cls }), n.text),
  );
  const w = $('#warnings');
  w.replaceChildren(...(st.warnings || []).map(w => h('p', {}, tErr(w))));
}

// ---------- clips ----------

async function loadGroups() {
  try {
    S.groups = await api('groups');
    S.loaded = true;
    if (S.view === 'clips') {
      if (!S.groups.length || $('#nothing', app())) {
        showClips(null);
      } else {
        renderList();
        if (!S.player && !location.hash.startsWith('#/clip/')) showClips(null);
      }
    }
  } catch (e) { console.warn(e); }
}

function onStatus() {
  renderPulse();
  const a = S.status.archive || {};
  const key = `${a.files}|${a.uploaded}|${a.bytes}`;
  if (key !== S.groupsKey) {
    S.groupsKey = key;
    clearTimeout(onStatus.t);
    onStatus.t = setTimeout(loadGroups, S.loaded ? 1000 : 0);
  }
  if (S.view === 'status') updateStatus();
  if (S.view === 'clips') updateNothing();
}

const FILTERS = [['all', t('All')], ['sentry', 'Sentry'], ['saved', t('Saved')], ['recent', t('Drives')], ['track', t('Track')]];

// Shown instead of the list and player while nothing has been preserved.
function nothingYet() {
  const box = h('div', { class: 'nothing', id: 'nothing' },
    h('h2', {}, t('No clips yet')),
    h('p', {}, t('Once the car records, drives and Sentry events show up here within a few minutes. This is where things stand:')),
    h('dl', { class: 'readout' }));
  updateNothing(box);
  return box;
}

function updateNothing(box = $('#nothing')) {
  const st = S.status;
  if (!box || !st) return;
  const u = usbState(st);
  const p = st.preserve || {};
  $('dl', box).replaceChildren(...[
    [t('USB drive'), stateLine(u.cls, u.text)],
    [t('Last scan'), p.last_scan ? ago(p.last_scan) : t('not yet')],
    [t('On the USB drive'), count(p.files_on_drive, 'file', 'files')],
  ].flatMap(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]));
}

function showClips(id) {
  S.view = 'clips';
  if (S.loaded && !S.groups.length) {
    if (S.player) { S.player.destroy(); S.player = null; }
    if (!$('#nothing', app())) app().replaceChildren(nothingYet());
    return;
  }
  if ($('#nothing', app())) app().replaceChildren();
  let root = $('.clips', app());
  if (!root) {
    root = h('div', { class: 'clips' },
      h('section', { class: 'list-pane', 'aria-label': 'Clips' },
        h('div', { class: 'filters', role: 'group', 'aria-label': t('Kind') },
          FILTERS.map(([k, label]) => h('button', {
            type: 'button', 'data-k': k, 'aria-pressed': String(S.filter === k),
            onclick: () => {
              S.filter = k;
              try { localStorage.setItem('filter', k); } catch {}
              renderList();
            },
          }, label))),
        h('div', { class: 'list' })),
      h('section', { class: 'player-pane', 'aria-label': t('Player') }));
    app().replaceChildren(root);
    renderList();
  }
  root.classList.toggle('has-player', !!id);
  for (const a of root.querySelectorAll('.clip')) a.setAttribute('aria-current', String(a.dataset.id === id));
  const pane = $('.player-pane', root);
  if (S.player && S.player.g.id === id) return;
  if (S.player) { S.player.destroy(); S.player = null; }
  if (!id) {
    pane.replaceChildren(S.groups.length
      ? h('div', { class: 'empty' }, h('h3', {}, t('Choose a clip')), t('Sentry events, saved clips and drives are on the left, newest first.'))
      : h('div'));
    return;
  }
  pane.replaceChildren(h('div', { class: 'empty' }, t('Loading…')));
  api('group?id=' + encodeURIComponent(id)).then(g => {
    if (location.hash !== '#/clip/' + encodeURIComponent(id)) return;
    S.player = new Player(pane, g);
  }).catch(() => {
    pane.replaceChildren(h('div', { class: 'empty' }, h('h3', {}, t('Clip not found')),
      t('It may just have been deleted to make room.') + ' ', h('a', { href: '#/' }, t('Back to the list'))));
  });
}

function renderList() {
  const root = $('.clips', app());
  if (!root) return;
  for (const b of root.querySelectorAll('.filters button')) b.setAttribute('aria-pressed', String(b.dataset.k === S.filter));
  const list = $('.list', root);
  const groups = S.groups.filter(g => S.filter === 'all' || g.kind === S.filter);
  if (!groups.length) {
    list.replaceChildren(!S.loaded ? h('div', { class: 'empty' }, t('Loading…')) : S.groups.length
      ? h('div', { class: 'empty' }, t('Nothing in this category.'))
      : h('div', { class: 'empty' }, h('h3', {}, t('No clips kept yet')),
        t('Once the car records, drives and Sentry events show up here within a few minutes.')));
    return;
  }
  const current = decodeURIComponent((location.hash.match(/^#\/clip\/(.+)$/) || [])[1] || '');
  const out = [];
  let lastDay = '';
  for (const g of groups) {
    const start = naive(g.start);
    const day = start ? dayLabel(start) : t('unknown date');
    if (day !== lastDay) { out.push(h('h3', { class: 'day' }, day)); lastDay = day; }
    const span = clipSpan(g);
    const where = g.kind === 'recent' ? duration(span) : (g.event && g.event.city) || (g.segment_count ? duration(span) : '');
    out.push(h('a', {
      class: 'clip kind-' + g.kind, href: '#/clip/' + encodeURIComponent(g.id), 'data-id': g.id,
      'aria-current': String(g.id === current),
    },
      h('span', { class: 'thumb' }, g.thumb
        ? h('img', { src: mediaURL(g.thumb), alt: '', loading: 'lazy' })
        : svg(ICON.drive)),
      h('span', { class: 'what' }, clipTitle(g)),
      h('span', { class: 'when' }, start ? hhmm(start) : ''),
      h('span', { class: 'where' }, where),
      h('span', { class: 'synced' }, g.uploaded && g.uploaded === g.files ? t('on NAS') : ''),
    ));
  }
  list.replaceChildren(...out);
}

// ---------- player ----------

class Player {
  constructor(pane, g) {
    this.g = g;
    this.segs = g.segments || [];
    this.cams = g.cameras || [];
    this.start = naive(this.segs.length ? this.segs[0].time : g.start);
    this.offsets = this.segs.map(s => (naive(s.time) - this.start) / 1000);
    this.dur = this.segs.map(() => 60);
    this.seg = 0;
    this.playing = false;
    this.rate = 1;
    this.focus = this.cams.includes('front') ? 'front' : this.cams[0];
    this.single = matchMedia('(max-width: 860px)').matches;
    this.videos = {};
    this.tiles = {};
    this.dragging = null;
    const ev = naive(g.event && g.event.timestamp);
    this.eventAt = ev && this.start ? (ev - this.start) / 1000 : null;
    this.onKey = this.onKey.bind(this);
    this.tick = this.tick.bind(this);
    this.build(pane);
    if (!this.segs.length) return;
    // Sentry and saved clips: start a little before the moment that
    // triggered them.
    const t0 = this.eventAt > 0 ? Math.max(0, this.eventAt - 15) : 0;
    this.pendingAbs = t0;
    this.loadLong().then(() => this.seekAbs(t0));
    document.addEventListener('keydown', this.onKey);
    this.raf = requestAnimationFrame(this.tick);
  }

  build(pane) {
    const g = this.g;
    const start = naive(g.start), end = naive(g.end);
    const sub = [];
    if (start) sub.push(dayLabel(start) + ', ' + hhmm(start) + (end ? '–' + hhmm(end) : ''));
    if (g.event && g.event.city) sub.push(g.event.city);
    const title = h('div', { class: 'title' }, h('h2', {}, clipTitle(g)), h('p', {}, sub.join(', ')));

    const actions = h('div', { class: 'actions' },
      h('a', { class: 'btn back', href: '#/' }, t('Back')),
      this.dlBtn = h('a', { class: 'btn', download: true, title: t('The chosen camera as one video') }, t('Download video')),
      h('a', { class: 'btn', href: 'api/download?id=' + encodeURIComponent(g.id), download: true }, t('Download all')),
      h('button', { class: 'btn danger', type: 'button', onclick: () => this.remove() }, t('Delete')));

    const stage = h('div', { class: 'stage' + (this.single ? ' single' : '') });
    for (const cam of this.cams) {
      const v = h('video', { muted: true, playsinline: true, preload: 'auto' });
      v.muted = true;
      v.addEventListener('loadedmetadata', () => this.onMeta(v));
      v.addEventListener('ended', () => { if (v === this.master()) this.pause(); });
      const tile = h('div', { class: 'tile', 'data-cam': cam, title: CAM_LABEL[cam] || cam },
        v, h('span', { class: 'cam' }, CAM_LABEL[cam] || cam), h('span', { class: 'none', hidden: true }, t('No video')));
      tile.addEventListener('click', () => this.zoom(cam));
      this.videos[cam] = v;
      this.tiles[cam] = tile;
      stage.append(tile);
    }
    this.clock = h('div', { class: 'clock', 'aria-live': 'off' });
    this.car = this.drawCar();
    this.hud = this.buildHud();
    // The car picks the camera when only one is shown.
    stage.append(this.hud.groupL, this.hud.groupR, h('div', { class: 'car-cell' }, this.car));
    stage.classList.toggle('six', this.cams.includes('left_pillar') || this.cams.includes('right_pillar'));
    this.stage = stage;

    this.playBtn = h('button', { class: 'icon-btn play', type: 'button', 'aria-label': t('Play'), onclick: () => this.toggle() });
    this.playBtn.innerHTML = ICON.play;
    const back = h('button', { class: 'icon-btn', type: 'button', 'aria-label': t('Back 10 seconds'), onclick: () => this.skip(-10) });
    back.innerHTML = ICON.back10;
    const fwd = h('button', { class: 'icon-btn', type: 'button', 'aria-label': t('Forward 10 seconds'), onclick: () => this.skip(10) });
    fwd.innerHTML = ICON.fwd10;

    this.timeline = this.buildTimeline();
    const speed = h('select', { class: 'speed', 'aria-label': t('Speed'), onchange: e => this.setRate(+e.target.value) },
      [0.5, 1, 1.5, 2, 4].map(r => h('option', { value: r, selected: r === 1 }, nf.format(r) + '×')));
    this.layoutBtn = h('button', { class: 'btn', type: 'button', onclick: () => this.setSingle(!this.single) });
    this.updateLayoutBtn();

    const transport = h('div', { class: 'transport' },
      this.clock,
      h('div', { class: 'controls' }, back, this.playBtn, fwd),
      this.timeline,
      h('div', { class: 'modes' }, speed, this.layoutBtn));

    const body = this.segs.length
      ? [stage, transport]
      : [h('div', { class: 'empty' }, t('This group has no video.') + ' ', (g.others || []).map(rel =>
          h('div', {}, h('a', { href: mediaURL(rel) + '?download' }, rel.split('/').pop()))))];
    pane.replaceChildren(h('div', { class: 'player' }, h('div', { class: 'player-head' }, title, actions), body));
    this.markFocus();
  }

  drawCar() {
    const dots = Object.entries(CAM_DOT).map(([cam, [x, y]]) =>
      `<circle class="camdot ${this.cams.includes(cam) ? 'has' : 'missing'}" data-cam="${cam}" cx="${x}" cy="${y}" r="6"><title>${CAM_LABEL[cam]}</title></circle>`).join('');
    const el = svg(`<svg class="car" viewBox="0 0 100 200" role="group" aria-label="${t('Choose a camera')}">
      <rect class="body" x="16" y="8" width="68" height="184" rx="30"/>
      <path class="glass" d="M26 58 Q50 46 74 58 L72 150 Q50 158 28 150 Z"/>
      <rect class="blink left" x="16" y="14" width="12" height="7" rx="3"/><rect class="blink left" x="16" y="180" width="12" height="7" rx="3"/>
      <rect class="blink right" x="72" y="14" width="12" height="7" rx="3"/><rect class="blink right" x="72" y="180" width="12" height="7" rx="3"/>
      <rect class="brake" x="32" y="184" width="36" height="5" rx="2"/>
      ${dots}</svg>`);
    el.addEventListener('click', e => {
      const cam = e.target.dataset && e.target.dataset.cam;
      if (cam && this.cams.includes(cam)) this.setFocus(cam);
    });
    return el;
  }

  // What the car recorded about itself (firmware 2025.44.25 and later, HW3
  // and up): speed, gear, Autopilot, blinkers, brake and position, a few
  // samples per second from the front camera's clip.
  buildHud() {
    // Two groups of instruments beside the front camera, like a cluster on
    // either side of the windscreen: what the car did (speed, gear,
    // Autopilot, route) and what the driver did (steering, blinkers,
    // pedals). With six cameras they form a strip between the rows.
    const pedal = (cls, label) => {
      const fill = h('i');
      return { fill, el: h('div', { class: 'pedal ' + cls }, h('span', { class: 'bar' }, fill), h('span', { class: 'lbl' }, label)) };
    };
    const hud = {
      speed: h('b', {}, '–'),
      gears: ['P', 'R', 'N', 'D'].map(g => h('span', { 'data-gear': g }, g)),
      ap: h('span', { class: 'ap', hidden: true }),
      map: h('a', { class: 'map', target: '_blank', rel: 'noopener', hidden: true }, t('Map')),
      route: svg('<svg class="route" viewBox="0 0 100 70" aria-hidden="true"><polyline/><circle r="3.5"/></svg>'),
      // The Model 3's and Y's wheel, turned around the centre of its rim.
      wheel: h('img', { class: 'wheel', src: 'wheel.svg', alt: '' }),
      angle: h('span', { class: 'angle' }, '–'),
      // The dashboard's turn signal symbol: a green block arrow.
      turnL: svg(`<svg class="turn" viewBox="0 0 26 30"><title>${t('Left blinker')}</title><path d="M0 15 15 0v9.5h11v11H15V30z"/></svg>`),
      turnR: svg(`<svg class="turn" viewBox="0 0 26 30"><title>${t('Right blinker')}</title><path d="M26 15 11 0v9.5H0v11h11V30z"/></svg>`),
      gas: pedal('gas', t('Throttle')),
      brake: pedal('brake', t('Brake')),
      none: h('p', { class: 'nodata', hidden: true }, t('No driving data in this recording')),
    };
    hud.groupL = h('div', { class: 'gauges gl' },
      h('div', { class: 'speedo' }, hud.speed, hud.unit = h('button', { class: 'unit', type: 'button', title: t('Switch between km/h and mph'),
        onclick: () => {
          SPEED_UNIT = SPEED_UNIT === UNITS.kmh ? UNITS.mph : UNITS.kmh;
          try { localStorage.setItem('speedUnit', SPEED_UNIT === UNITS.mph ? 'mph' : 'kmh'); } catch { /* only for this visit */ }
          hud.unit.textContent = SPEED_UNIT.label;
          hud.speed.textContent = '';
        } }, SPEED_UNIT.label)),
      h('div', { class: 'drive' }, h('div', { class: 'prnd', title: t('Gear') }, hud.gears), hud.ap),
      hud.route, hud.map, hud.none);
    hud.groupR = h('div', { class: 'gauges gr' },
      h('div', { class: 'steering' }, h('div', { class: 'steer' }, hud.turnL, hud.wheel, hud.turnR), hud.angle),
      h('div', { class: 'pedals' }, hud.gas.el, hud.brake.el));
    this.tel = {};
    return hud;
  }

  telRel(i) {
    const cams = this.segs[i] && this.segs[i].cams;
    return cams && (cams.front || cams[this.cams[0]]);
  }

  loadTel(i) {
    const rel = this.telRel(i);
    if (!rel || this.tel[i]) return;
    this.tel[i] = fetch('api/telemetry?rel=' + encodeURIComponent(rel)).then(r => r.ok ? r.json() : null)
      .then(d => {
        const samples = (d && d.samples) || [];
        this.tel[i] = Object.assign(samples, { dur: d && d.dur });
        for (const x of samples) this.pedalMax = Math.max(this.pedalMax || 0, x.p || 0);
      }).catch(() => { this.tel[i] = []; });
  }

  // sampleAt is the car's state at a moment in minute i. The samples cover
  // the recording itself (dur from the Pi), not the time the long video
  // holds its last frame. Steering, throttle and speed are interpolated
  // between the two samples around the moment, so with a call every frame
  // they move smoothly and in step with the video; the rest is taken from
  // the nearest sample.
  sampleAt(i, local) {
    const s = this.tel[i];
    if (!Array.isArray(s) || !s.length) return null;
    const dur = s.dur || this.dur[i] || 60;
    const x = Math.min(s.length - 1, Math.max(0, local / dur * s.length));
    const k = Math.floor(x), f = x - k;
    const a = s[k], b = s[Math.min(k + 1, s.length - 1)];
    const near = f < 0.5 ? a : b;
    const mix = key => (a[key] || 0) + ((b[key] || 0) - (a[key] || 0)) * f;
    return { ...near, s: mix('s'), p: mix('p'), v: mix('v') };
  }

  // updateHud runs every frame: it only touches the page where something
  // changed, or the browser redoes its layout sixty times a second.
  updateHud(local) {
    const hud = this.hud;
    const text = (el, v) => { if (el.textContent !== v) el.textContent = v; };
    const s = this.sampleAt(this.seg, local);
    // Known to have no data (older firmware, or parked): say so once.
    const known = Array.isArray(this.tel[this.seg]);
    const stage = this.stage;
    stage.classList.toggle('no-data', known && !s);
    hud.none.hidden = !(known && !s);
    this.car.classList.toggle('left-on', !!(s && s.bl));
    this.car.classList.toggle('right-on', !!(s && s.br));
    this.car.classList.toggle('braking', !!(s && s.b));
    hud.turnL.classList.toggle('on', !!(s && s.bl));
    hud.turnR.classList.toggle('on', !!(s && s.br));
    if (!s) return;
    text(hud.speed, String(Math.round((s.v || 0) * SPEED_UNIT.factor)));
    const gear = ['P', 'D', 'R', 'N'][s.g || 0];
    for (const g of hud.gears) g.classList.toggle('on', g.dataset.gear === gear);
    text(hud.ap, [, t('Self-driving'), t('Autosteer'), t('Cruise')][s.a || 0] || '');
    hud.ap.hidden = !s.a;
    // The car reports the wheel's angle positive to the right (clockwise,
    // as CSS rotates), checked against a real drive.
    const deg = s.s || 0;
    hud.wheel.style.transform = `rotate(${deg.toFixed(1)}deg)`;
    const a = Math.round(Math.abs(deg));
    text(hud.angle, a < 1 ? '0°' : `${a}° ${deg > 0 ? t('right') : t('left')}`);
    // The car reports the accelerator from 0 to 100, or 0 to 1 in older
    // data. Which one is decided for the whole drive: per sample, a light
    // press (0.4 of 100) and every start of a press, interpolated up from
    // 0, would read as nearly full.
    const scale = (this.pedalMax || 0) > 1 ? 1 : 100;
    const p = Math.max(0, Math.min(100, (s.p || 0) * scale));
    hud.gas.fill.style.height = p.toFixed(0) + '%';
    hud.gas.el.classList.toggle('on', p >= 1);
    hud.brake.fill.style.height = s.b ? '100%' : '0';
    hud.brake.el.classList.toggle('on', !!s.b);
    const gps = s.la || s.lo;
    hud.map.hidden = !gps;
    // The link follows the position at a few metres; a new one every frame
    // is of no use to anyone.
    if (gps) {
      const [la, lo] = [s.la.toFixed(4), s.lo.toFixed(4)];
      if (hud.map.dataset.at !== la + lo) {
        hud.map.dataset.at = la + lo;
        hud.map.href = `https://www.openstreetmap.org/?mlat=${la}&mlon=${lo}#map=17/${la}/${lo}`;
      }
    }
    this.drawRoute(s);
  }

  // The route of the minutes loaded so far, as a line without a map: in
  // the car there is often no internet for map tiles.
  drawRoute(cur) {
    const route = this.hud.route;
    // The line and its scale change only when another minute's data has
    // come in; this runs every frame.
    const loaded = this.segs.reduce((n, _, i) => n + (Array.isArray(this.tel[i]) ? 1 : 0), 0);
    if (loaded !== this.routeLoaded) {
      this.routeLoaded = loaded;
      const pts = [];
      for (let i = 0; i < this.segs.length; i++) {
        if (Array.isArray(this.tel[i])) for (const s of this.tel[i]) if (s.la || s.lo) pts.push([s.lo, s.la]);
      }
      this.routeXY = null;
      if (pts.length >= 2) {
        let [x0, y0, x1, y1] = [Infinity, Infinity, -Infinity, -Infinity];
        for (const [x, y] of pts) { x0 = Math.min(x0, x); x1 = Math.max(x1, x); y0 = Math.min(y0, y); y1 = Math.max(y1, y); }
        const k = Math.cos(((y0 + y1) / 2) * Math.PI / 180);
        const scale = Math.min(92 / Math.max((x1 - x0) * k, 1e-6), 62 / Math.max(y1 - y0, 1e-6));
        const ox = (100 - (x1 - x0) * k * scale) / 2, oy = (70 - (y1 - y0) * scale) / 2;
        this.routeXY = ([x, y]) => [ox + (x - x0) * k * scale, oy + (y1 - y) * scale];
        route.querySelector('polyline').setAttribute('points', pts.map(p => this.routeXY(p).map(n => n.toFixed(1)).join(',')).join(' '));
      }
    }
    route.hidden = !this.routeXY;
    if (!this.routeXY || !(cur.la || cur.lo)) return;
    const [cx, cy] = this.routeXY([cur.lo, cur.la]);
    const c = route.querySelector('circle');
    c.setAttribute('cx', cx.toFixed(1));
    c.setAttribute('cy', cy.toFixed(1));
  }

  buildTimeline() {
    const tl = h('div', { class: 'timeline', role: 'slider', tabindex: '0', 'aria-label': t('Timeline') });
    this.road = h('div', { class: 'road' });
    this.done = h('div', { class: 'done' });
    this.road.append(this.done);
    this.head = h('div', { class: 'head' });
    this.ends = h('div', { class: 'ends' });
    tl.append(this.road, this.head, this.ends);
    const frac = e => {
      const r = this.road.getBoundingClientRect();
      return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width));
    };
    tl.addEventListener('pointerdown', e => {
      tl.setPointerCapture(e.pointerId);
      this.dragging = frac(e) * this.total();
    });
    tl.addEventListener('pointermove', e => { if (this.dragging != null) this.dragging = frac(e) * this.total(); });
    tl.addEventListener('pointerup', e => {
      if (this.dragging == null) return;
      // Seek only on release: every seek is a range request to the Pi.
      const t = frac(e) * this.total();
      this.dragging = null;
      this.seekAbs(t);
    });
    tl.addEventListener('keydown', e => {
      if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
        e.preventDefault();
        e.stopPropagation();
        this.skip(e.key === 'ArrowLeft' ? -5 : 5);
      }
    });
    this.renderMarks();
    return tl;
  }

  renderMarks() {
    const total = this.total();
    for (const el of this.road.querySelectorAll('.seg')) el.remove();
    this.offsets.forEach((o, i) => {
      if (i) this.road.append(h('div', { class: 'seg', style: `left:${(o / total) * 100}%` }));
    });
    const old = $('.event', this.timeline || document.createElement('div'));
    if (old) old.remove();
    if (this.eventAt != null && this.eventAt >= 0 && this.timeline) {
      this.timeline.append(h('div', { class: 'event', style: `left:${(this.eventAt / total) * 100}%`, title: t('Moment of the event') }));
    }
    if (this.start) {
      this.ends.replaceChildren(h('span', {}, hhmmss(this.start)), h('span', {}, hhmmss(new Date(+this.start + total * 1000))));
    }
  }

  total() {
    const n = this.segs.length;
    return n ? this.offsets[n - 1] + this.dur[n - 1] : 0;
  }

  // Every camera plays one continuous video of the whole group, which the
  // Pi assembles from the minute files: no reloading between minutes.
  // this.long[cam][i] says where minute i is in that camera's video.
  async loadLong() {
    try {
      this.long = (await api('long?id=' + encodeURIComponent(this.g.id))).cams || {};
    } catch {
      this.long = {};
    }
    for (const [i, seg] of (this.long[this.masterCam()] || []).entries()) {
      if (seg && seg.ok) this.dur[i] = seg.dur;
    }
    this.renderMarks();
    this.attach();
  }

  longURL(cam) {
    return 'long/' + encodeURIComponent(cam) + '/' + this.g.id.split('/').map(encodeURIComponent).join('/') + '.mp4';
  }

  has(cam) { return !!(this.long && this.long[cam] && this.long[cam].some(s => s && s.ok)); }

  masterCam() {
    for (const cam of [this.focus, 'front', ...this.cams]) if (this.has(cam)) return cam;
    return this.focus;
  }

  master() {
    const v = this.videos[this.masterCam()];
    return v && v.dataset.src ? v : null;
  }

  // The cameras that play: all of them, or only the chosen one.
  attach() {
    if (!this.long) return;
    for (const cam of this.cams) {
      const v = this.videos[cam];
      const want = this.has(cam) && (!this.single || cam === this.focus)
        ? this.longURL(cam) : '';
      this.tiles[cam].querySelector('.none').hidden = this.has(cam);
      if (v.dataset.src === want) continue;
      v.dataset.src = want;
      if (want) v.src = want;
      else { v.removeAttribute('src'); v.load(); }
    }
  }

  // segAt returns the minute that holds group time t.
  segAt(t) {
    let i = 0;
    while (i + 1 < this.segs.length && this.offsets[i + 1] <= t) i++;
    return i;
  }

  // toLong is where group time t is in a camera's video, or null when that
  // camera has no recording then.
  toLong(cam, t) {
    const i = this.segAt(t);
    const s = this.long && this.long[cam] && this.long[cam][i];
    if (!s || !s.ok) return null;
    const local = t - this.offsets[i];
    return local > s.dur + 0.5 ? null : s.start + Math.max(0, Math.min(local, s.dur - 0.05));
  }

  // fromLong is the group time of position x in a camera's video.
  fromLong(cam, x) {
    const segs = (this.long && this.long[cam]) || [];
    let best = null;
    segs.forEach((s, i) => { if (s && s.ok && s.start <= x + 0.001) best = [i, s]; });
    if (!best) return { i: 0, local: 0 };
    return { i: best[0], local: Math.min(x - best[1].start, best[1].dur) };
  }

  onMeta(v) {
    if (this.pendingAbs != null) this.place(v);
    if (v === this.master() && this.playing) this.playAll();
  }

  // place puts one video at the pending group time.
  place(v) {
    const cam = Object.keys(this.videos).find(c => this.videos[c] === v);
    const x = this.toLong(cam, this.pendingAbs);
    if (x != null && v.readyState >= 1) v.currentTime = x;
  }

  seekAbs(t) {
    if (!this.segs.length) return;
    t = Math.max(0, Math.min(t, this.total() - 0.05));
    // A moment the main camera has no recording of: on to its next one.
    const m = this.masterCam();
    if (this.long && this.toLong(m, t) == null) {
      const i = this.segs.findIndex((_, j) => j >= this.segAt(t) && this.long[m] && this.long[m][j] && this.long[m][j].ok);
      if (i >= 0) t = this.offsets[i];
    }
    this.pendingAbs = t;
    this.seg = this.segAt(t);
    this.loadTel(this.seg);
    this.loadTel(this.seg + 1);
    for (const v of Object.values(this.videos)) if (v.dataset.src) this.place(v);
    const mv = this.master();
    if (mv && mv.readyState >= 1) this.pendingAbs = null;
  }

  now() {
    const m = this.master();
    if (!m || this.pendingAbs != null) return this.pendingAbs || 0;
    const p = this.fromLong(this.masterCam(), m.currentTime);
    return this.offsets[p.i] + p.local;
  }

  skip(d) { this.seekAbs(this.now() + d); }

  playAll() {
    for (const v of Object.values(this.videos)) {
      if (!v.dataset.src) continue;
      v.playbackRate = this.rate;
      v.play().catch(() => {});
    }
  }

  toggle() { this.playing ? this.pause() : this.play(); }

  play() {
    this.playing = true;
    this.playBtn.innerHTML = ICON.pause;
    this.playBtn.setAttribute('aria-label', t('Pause'));
    if (this.now() >= this.total() - 0.2) this.seekAbs(0);
    this.playAll();
  }

  pause() {
    this.playing = false;
    this.playBtn.innerHTML = ICON.play;
    this.playBtn.setAttribute('aria-label', t('Play'));
    for (const v of Object.values(this.videos)) v.pause();
  }

  setRate(r) {
    this.rate = r;
    for (const v of Object.values(this.videos)) v.playbackRate = r;
  }

  setFocus(cam) {
    if (cam === this.focus) return;
    const t = this.now();
    this.focus = cam;
    this.markFocus();
    if (this.single) { this.attach(); this.seekAbs(t); }
  }

  zoom(cam) {
    if (matchMedia('(max-width: 860px)').matches) { this.toggle(); return; }
    if (this.single && cam === this.focus) { this.setSingle(false); return; }
    this.focus = cam;
    this.markFocus();
    this.setSingle(true);
  }

  setSingle(on) {
    const t = this.now();
    this.single = on;
    this.stage.classList.toggle('single', on);
    this.updateLayoutBtn();
    this.attach();
    this.seekAbs(t);
  }

  updateLayoutBtn() {
    this.layoutBtn.textContent = this.single ? t('All cameras') : t('One camera');
  }

  markFocus() {
    if (this.dlBtn) this.dlBtn.href = this.longURL(this.focus) + '?download';
    for (const [cam, tile] of Object.entries(this.tiles)) tile.classList.toggle('focus', cam === this.focus);
    for (const dot of this.car.querySelectorAll('.camdot')) dot.classList.toggle('active', dot.dataset.cam === this.focus);
  }

  tick() {
    this.raf = requestAnimationFrame(this.tick);
    const m = this.master();
    const total = this.total();
    const abs = this.dragging != null ? this.dragging : this.now();
    const pct = total ? (abs / total) * 100 : 0;
    this.head.style.left = pct + '%';
    this.done.style.width = pct + '%';
    this.timeline.setAttribute('aria-valuenow', Math.round(abs));
    if (this.start) {
      const at = new Date(+this.start + abs * 1000);
      if (this.clock.dataset.t !== hhmmss(at)) {
        this.clock.dataset.t = hhmmss(at);
        this.clock.replaceChildren(hhmmss(at), h('small', {}, dayLabel(at)));
      }
    }
    // A video that stops getting data can wait for ever; seeking to the same
    // place makes the browser ask again. After 3 s without progress while
    // playing, do that.
    if (m && this.playing && !m.seeking && this.pendingAbs == null) {
      const now = performance.now();
      if (m.currentTime !== this.lastTime) {
        this.lastTime = m.currentTime;
        this.lastMove = now;
      } else if (now - (this.lastMove || now) > 3000) {
        this.lastMove = now;
        this.seekAbs(this.now());
        this.playAll();
        return;
      }
    }
    if (!m || m.readyState < 1 || m.seeking || this.pendingAbs != null) return;
    const i = this.segAt(abs);
    if (i !== this.seg) {
      this.seg = i;
      this.loadTel(i);
      this.loadTel(i + 1);
    }
    if (this.dragging == null) this.updateHud(abs - this.offsets[i]);
    if (m.readyState < 2) return;
    for (const [cam, v] of Object.entries(this.videos)) {
      if (v === m || !v.dataset.src || v.readyState < 1 || v.seeking) continue;
      const x = this.toLong(cam, abs);
      // No recording of this camera at this moment.
      this.tiles[cam].querySelector('.none').hidden = x != null;
      if (x == null) { if (!v.paused) v.pause(); continue; }
      // A camera that runs behind or ahead catches up by playing a little
      // faster or slower; seeking it makes it load again and fall behind
      // further, which on a Pi's wifi it then keeps doing.
      const d = v.currentTime - x;
      if (m.paused) {
        if (Math.abs(d) > 0.05) v.currentTime = x;
      } else if (Math.abs(d) > 1.5) {
        v.currentTime = x;
        v.playbackRate = this.rate;
      } else if (Math.abs(d) > 0.15) {
        v.playbackRate = this.rate * (d > 0 ? 0.92 : 1.08);
      } else if (v.playbackRate !== this.rate) {
        v.playbackRate = this.rate;
      }
      if (!m.paused && v.paused && !v.ended && v.readyState >= 2) v.play().catch(() => {});
      if (m.paused && !v.paused) v.pause();
    }
  }

  onKey(e) {
    if (e.target.closest('input, select, textarea')) return;
    if (e.key === ' ' && !e.target.closest('button, a')) { e.preventDefault(); this.toggle(); }
    else if (e.key === 'ArrowLeft') this.skip(-5);
    else if (e.key === 'ArrowRight') this.skip(5);
  }

  async remove() {
    const what = { sentry: 'Delete this Sentry event?', saved: 'Delete this saved clip?', recent: 'Delete this drive?', track: 'Delete these track recordings?' }[this.g.kind] || 'Delete these clips?';
    if (!confirm(t(what) + ' ' + t('The files are removed from the Pi and are not taken from the USB drive again.'))) return;
    try {
      await api('group?id=' + encodeURIComponent(this.g.id), { method: 'DELETE' });
      S.groups = S.groups.filter(g => g.id !== this.g.id);
      location.hash = '#/';
      loadGroups();
    } catch (e) { alert(t('Deleting failed: {0}', tErr(e.message))); }
  }

  destroy() {
    cancelAnimationFrame(this.raf);
    document.removeEventListener('keydown', this.onKey);
    for (const v of Object.values(this.videos)) { v.pause(); v.removeAttribute('src'); v.load(); }
  }
}

// ---------- status ----------

const USB_STATE = {
  configured: t('In use by the car'), suspended: t('Car is asleep'), 'not attached': t('Not connected'),
  attached: t('Connected'), powered: t('Powered, not recognised yet'), default: t('Being recognised'), addressed: t('Being recognised'),
};

function readout(rows) {
  return h('dl', { class: 'readout' }, rows.filter(Boolean).map(([k, v]) => [h('dt', {}, k), h('dd', {}, v)]));
}

function stateLine(cls, text) {
  return h('span', { class: 'state' }, h('i', { class: 'dot ' + cls }), text);
}

function showStatus() {
  S.view = 'status';
  const section = (key, title, note, ...extra) => h('section', { class: 'section' },
    h('header', {}, h('h3', {}, title), note ? h('p', {}, note) : null), h('div', { 'data-k': key }), extra);
  const repairOut = h('pre', { class: 'output', hidden: true });
  const repairBtn = h('button', { class: 'btn', type: 'button', onclick: async () => {
    if (!confirm(t('The USB drive is disconnected from the car for about a minute while its file system is checked. Do not do this while driving. Continue?'))) return;
    repairBtn.disabled = true;
    repairOut.hidden = false;
    repairOut.textContent = t('Checking…');
    try {
      const r = await api('repair', { method: 'POST' });
      repairOut.textContent = r.output || t('Done, nothing to report.');
    } catch (e) {
      repairOut.textContent = t('Failed: {0}', tErr(e.message)) + '\n\n' + ((e.data && e.data.output) || '');
    }
    repairBtn.disabled = false;
  } }, t('Check file system'));

  const action = (label, path) => h('button', { class: 'btn', type: 'button', onclick: async e => {
    e.target.disabled = true;
    try { await api(path, { method: 'POST' }); } catch (err) { alert(tErr(err.message)); }
    setTimeout(() => { e.target.disabled = false; }, 1500);
  } }, label);

  app().replaceChildren(h('div', { class: 'page' },
    h('h2', {}, 'Status'),
    h('div', { class: 'overview', 'data-k': 'overview' }),
    section('usb', t('Car'), t('The USB drive the car sees.')),
    section('storage', t('Storage'), t('When space runs low, clips already on the NAS are cleared first, then the oldest drives.')),
    section('preserve', t('Keeping clips'), t('Every finished clip is copied off the USB drive before the car throws it away.'),
      h('div', { class: 'row-actions' }, action(t('Scan now'), 'scan'))),
    section('nas', 'NAS', null, h('div', { class: 'row-actions' }, action(t('Sync now'), 'nas/sync'))),
    section('system', t('System'), null, h('div', { class: 'row-actions' }, h('button', { class: 'btn', type: 'button', onclick: async () => {
      if (confirm(t('Restart the Pi? The car will not see the USB drive for a moment.'))) { try { await api('reboot', { method: 'POST' }); } catch {} }
    } }, t('Restart Pi')))),
    h('section', { class: 'section' },
      h('header', {}, h('h3', {}, t('Maintenance')), h('p', {}, t('Checks and repairs the file system of the USB drive. Use this when the car reports a problem with the USB drive.'))),
      h('div', { class: 'row-actions', style: 'grid-column:auto;margin:0' }, repairBtn), repairOut),
    camSizeSection(),
    computerSection(),
  ));
  updateStatus();
}

// Computer mode: plugged into a computer, the Pi is a read-only USB drive
// with each camera of each drive or event as one long video. The car's
// drive is gone meanwhile; a restart is always car mode.
function computerSection() {
  const state = h('p', { style: 'margin:0' });
  const btn = h('button', { class: 'btn', type: 'button', onclick: async () => {
    const on = !(S.status && S.status.computer && S.status.computer.state !== 'off');
    if (on && !confirm(t('The car loses its USB drive until you switch back or restart the Pi. Do this with the Pi connected to a computer, not in the car. Continue?'))) return;
    btn.disabled = true;
    try {
      await api('computer', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ on, lang: LANG }) });
    } catch (e) { alert(tErr(e.message)); }
    btn.disabled = false;
  } });
  state.update = () => {
    const c = (S.status && S.status.computer) || { state: 'off' };
    btn.textContent = c.state === 'off' ? t('Switch to computer mode') : t('Back to car mode');
    btn.disabled = c.state === 'building';
    state.textContent = {
      off: c.error ? t('Failed: {0}', tErr(c.error)) : t("Off: the USB drive is the car's."),
      building: t('Preparing the drive… {0} of {1}', c.done || 0, c.total || '?'),
      on: t('On: {0} files, {1}. Connect the Pi to your computer; it shows up as the drive Squirrel.', c.files, bytes(c.size)),
    }[c.state] || '';
  };
  state.update();
  return h('section', { class: 'section' },
    h('header', {}, h('h3', {}, t('Computer mode')),
      h('p', {}, t('Turns the Pi into a read-only USB drive for your computer, with every camera of every drive and event as one long video. Copying over USB is much faster than over wifi.'))),
    h('div', { class: 'fields' }, h('div', { 'data-k': 'computer' }, state),
      h('div', { class: 'row-actions', style: 'grid-column:auto;margin:0' }, btn)));
}

// Changing the room for the car means making a new drive: exFAT cannot be
// resized. The Pi preserves what is on the old one first.
function camNow() {
  const st = S.status || {};
  const total = (st.disk || {}).total || 0;
  const current = (st.image || {}).size || 0;
  return { current, pct: total && current ? Math.round((current / total) * 100) : 0 };
}

function camSizeSection() {
  let now = camNow().pct;
  const pct = h('output', {}, '');
  const nowText = h('p', { style: 'margin:0', 'data-k': 'camnow' });
  const slider = h('input', { type: 'range', min: 20, max: 60, step: 5, value: 40,
    'aria-label': t('Space for the car'), oninput: () => { pct.textContent = slider.value + '%'; slider.dataset.touched = '1'; btn.disabled = +slider.value === now; } });
  // Fill in once the status is known, without undoing a choice.
  nowText.update = () => {
    const c = camNow();
    if (!c.current) { nowText.textContent = t('Size not known yet.'); return; }
    now = c.pct;
    nowText.textContent = c.pct ? t('Now {0}, about {1}% of the card.', bytes(c.current), c.pct) : t('Now {0}, less than 1% of the card.', bytes(c.current));
    if (!slider.dataset.touched) {
      slider.value = Math.min(60, Math.max(20, Math.round(c.pct / 5) * 5));
      pct.textContent = slider.value + '%';
      btn.disabled = true;
    }
  };
  const out = h('pre', { class: 'output', hidden: true });
  const btn = h('button', { class: 'btn', type: 'button', disabled: true, onclick: async () => {
    const p = +slider.value;
    if (!confirm(t('The car\'s USB drive is made again at {0}% of the card.', p) + ' ' +
      t('The Pi first keeps everything on it; then the drive is empty and gone from the car for about a minute. Do not do this while driving. Continue?'))) return;
    btn.disabled = true;
    out.hidden = false;
    out.textContent = t('Keeping clips and making the new drive…');
    try {
      const r = await api('camsize', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ percent: p }) });
      out.textContent = tErr(r.output);
    } catch (e) {
      out.textContent = t('Failed: {0}', tErr(e.message)) + '\n\n' + tErr((e.data && e.data.output) || '');
    }
  } }, t('Make drive again'));
  return h('section', { class: 'section' },
    h('header', {}, h('h3', {}, t('Space for the car')),
      h('p', {}, t('How much of the card the car gets as its USB drive. The rest is for the clips the Pi keeps.'))),
    h('div', { class: 'fields' },
      nowText,
      h('label', { class: 'field' }, t('New size'), h('span', { class: 'sliderrow' }, slider, pct)),
      h('div', { class: 'row-actions', style: 'grid-column:auto;margin:0' }, btn), out));
}

function updateStatus() {
  const st = S.status;
  const cn = $('[data-k="camnow"]', app());
  if (cn && cn.update) cn.update();
  const cm = $('[data-k="computer"] p', app());
  if (cm && cm.update) cm.update();
  const fill = (k, node) => { const el = $(`[data-k="${k}"]`, app()); if (el) el.replaceChildren(node); };
  if (st) {
    const u0 = usbState(st);
    const d0 = st.disk || {};
    const n0 = st.nas || {};
    const ns0 = nasState(n0);
    const a0 = st.activity || {};
    const cell = (what, state, sub) => h('div', {}, h('div', { class: 'what' }, what), state, h('div', { class: 'sub' }, sub));
    const ov = $('[data-k="overview"]', app());
    if (ov) ov.replaceChildren(
      cell(t('Car'), stateLine(u0.cls, u0.text),
        a0.last_write ? t('Last write {0}', ago(a0.last_write)) : t('Nothing written since the start')),
      cell(t('Storage'), stateLine(d0.free > (st.space ? st.space.reserve : 0) ? 'ok' : 'warn', t('{0} free', bytes(d0.free))),
        t('of {0}', bytes(d0.total))),
      cell('NAS', stateLine(ns0.cls, ns0.text),
        n0.state === 'disabled' ? t('Uploading is off') : n0.last_success ? t('Last success {0}', ago(n0.last_success)) : t('{0} to go', count(n0.pending, 'file', 'files'))));
  }
  if (!st) { fill('usb', h('p', { class: 'muted' }, t('Waiting for the Pi…'))); return; }

  const u = usbState(st);
  const g = st.gadget || {};
  fill('usb', readout([
    ['Status', stateLine(u.cls, g.bound ? (USB_STATE[g.state] || g.state || t('Unknown')) : t('USB gadget is off'))],
    [t('Writing'), st.activity ? rate(st.activity.write_bps) + (st.activity.last_write ? ', ' + t('last {0}', ago(st.activity.last_write)) : '') : '–'],
    [t('Reading'), st.activity ? rate(st.activity.read_bps) : '–'],
    ['Controller', g.udc || '–'],
    st.image && st.image.path && [t('Drive image'), h('span', { class: 'quiet' }, t('{0}, {1} ({2} in use)', st.image.path, bytes(st.image.size), bytes(st.image.allocated)))],
  ]));

  const d = st.disk || {};
  const used = d.total ? (d.total - d.free) / d.total : 0;
  const reservePct = d.total && st.space ? Math.min(100, (st.space.reserve / d.total) * 100) : 0;
  const a = st.archive || {};
  fill('storage', h('div', {},
    h('div', { class: 'meter', role: 'img', 'aria-label': t('{0}% in use', Math.round(used * 100)) },
      h('i', { style: `width:${used * 100}%` }), h('b', { style: `left:calc(${100 - reservePct}% - 2px)`, title: t('Reserve limit') })),
    readout([
      [t('Free'), t('{0} of {1}', bytes(d.free), bytes(d.total))],
      [t('Reserve'), st.space ? bytes(st.space.reserve) : '–'],
      [t('Kept'), t('{0} and {1}, {2}, {3}', count(a.events, 'event', 'events'), count(a.drives, 'drive', 'drives'), count(a.files, 'file', 'files'), bytes(a.bytes))],
      [t('Cleared'), st.space && st.space.pruned_groups ? t('{0}, last {1}', count(st.space.pruned_groups, 'group', 'groups'), ago(st.space.last_prune)) : t('Nothing yet')],
    ])));

  const p = st.preserve || {};
  fill('preserve', readout([
    [t('Last scan'), p.last_scan ? `${ago(p.last_scan)} (${p.last_duration})` : (p.running ? t('Busy…') : t('Not yet'))],
    [t('USB drive'), p.fs_type ? t('{0}, clusters of {1}', p.fs_type === 'exfat' ? 'exFAT' : 'FAT32', bytes(p.cluster_size))
      // Set by whoever has the drive, so only worth a word while nobody has.
      + (p.dirty && (st.gadget || {}).state !== 'configured' ? ', ' + t('not closed cleanly') : '') : '–'],
    [t('On the USB drive'), t('{0}, of which {1} still recording or waiting', count(p.files_on_drive, 'file', 'files'), p.waiting || 0)],
    [t('Kept since start'), count(p.extracted, 'file', 'files')],
    [t('Method'), p.bytes_reflinked || p.bytes_copied
      ? t('{0} shared by reflink, {1} copied', bytes(p.bytes_reflinked), bytes(p.bytes_copied))
      : '–'],
    p.last_extracted && [t('Last kept'), p.last_extracted],
    p.last_error && [t('Last error'), `${tErr(p.last_error)} (${ago(p.last_error_at)})`],
  ]));

  const n = st.nas || {};
  const ns = nasState(n);
  fill('nas', readout([
    ['Status', stateLine(ns.cls, ns.text)],
    [t('Queue'), `${count(n.pending, 'file', 'files')}, ${bytes(n.pending_bytes)}`],
    n.current && [t('Working on'), t('{0} ({1} of {2}, {3})', n.current, bytes(n.current_done), bytes(n.current_size), rate(n.rate_bps))],
    [t('Last success'), ago(n.last_success)],
    n.last_error && !(n.last_success > n.last_error_at) && [t('Last error'), `${tErr(n.last_error)} (${ago(n.last_error_at)})`],
  ]));

  const s = st.system || {};
  fill('system', readout([
    [t('Device'), [s.model, s.hostname].filter(Boolean).join(', ') || '–'],
    [t('Network'), (s.addresses || []).join(', ') || '–'],
    st.wifi && st.wifi.available && ['Wifi', wifiText(st.wifi)],
    s.wifi_dbm && [t('Wifi signal'), `${s.wifi_dbm} dBm`],
    [t('Up for'), s.uptime ? duration(s.uptime) : '–'],
    s.temp_c && [t('Temperature'), `${nf.format(s.temp_c)} °C`],
    s.load && [t('Load'), s.load],
    s.mem_total && [t('Free memory'), t('{0} of {1}', bytes(s.mem_avail), bytes(s.mem_total))],
    [t('Version'), st.version],
  ]));
}

// ---------- settings ----------

async function showSettings() {
  S.view = 'settings';
  app().replaceChildren(h('div', { class: 'page' }, h('h2', {}, t('Settings')), h('p', { class: 'muted' }, t('Loading…'))));
  let cfg;
  try { cfg = await api('config'); } catch (e) {
    app().replaceChildren(h('div', { class: 'page' }, h('h2', {}, t('Settings')), h('p', { class: 'note bad' }, t('Could not load the settings: {0}', tErr(e.message)))));
    return;
  }
  if (S.view !== 'settings') return;

  const check = (obj, key, label, hint) => h('label', {},
    h('input', { type: 'checkbox', checked: !!obj[key], onchange: e => { obj[key] = e.target.checked; } }),
    h('span', {}, label, hint ? h('small', {}, ' ', hint) : null));
  const text = (obj, key, label, hint, type = 'text') => h('label', { class: 'field' }, label,
    h('input', { type, value: obj[key] ?? '', autocomplete: type === 'password' ? 'new-password' : 'off',
      oninput: e => { obj[key] = type === 'number' ? +e.target.value : e.target.value; } }),
    hint ? h('span', {}, hint) : null);
  const folders = obj => h('div', { class: 'checks' },
    check(obj, 'sentry', 'Sentry'), check(obj, 'saved', t('Saved clips')),
    check(obj, 'recent', t('Drives'), t('(everything recorded while driving; takes a lot of space)')), check(obj, 'track', t('Track mode')));

  const note = h('p', { class: 'note' });
  const nasNote = h('span', { class: 'note' });
  const section = (title, intro, ...body) => h('section', { class: 'section' },
    h('header', {}, h('h3', {}, title), intro ? h('p', {}, intro) : null), h('div', { class: 'fields' }, body));

  const form = h('form', { onsubmit: async e => {
    e.preventDefault();
    note.className = 'note';
    note.textContent = t('Saving…');
    try {
      await api('config', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cfg) });
      note.className = 'note good';
      note.textContent = t('Saved.');
    } catch (err) {
      note.className = 'note bad';
      note.textContent = t('Not saved: {0}', tErr(err.message));
    }
  } },
    section(t('Keeping clips'), t('Which clips the Pi copies off the USB drive. The car itself does not notice.'),
      folders(cfg.keep),
      text(cfg, 'scan_interval_sec', t('Scan interval in seconds'), t('How often the USB drive is read. Keep it short: the car deletes what it recorded while parked as soon as you drive off.'), 'number'),
      text(cfg, 'reserve_gb', t('Keep free (GB)'), t('Below this limit the Pi clears old clips, so the car can always keep writing.'), 'number')),
    section('NAS', t('Uploads over SMB whenever the NAS is in reach, usually when the car is at home.'),
      h('div', { class: 'checks' }, check(cfg.nas, 'enabled', t('Upload to NAS'))),
      text(cfg.nas, 'host', 'Server', t('Name or IP address, for example nas.local or 192.168.1.20')),
      text(cfg.nas, 'share', t('Share'), t('The name of the share, for example video')),
      text(cfg.nas, 'path', t('Folder in the share')),
      text(cfg.nas, 'user', t('User')),
      text(cfg.nas, 'password', t('Password'), null, 'password'),
      text(cfg.nas, 'domain', t('Domain'), t('Usually empty')),
      h('div', {}, h('p', { class: 'muted', style: 'margin:0 0 6px' }, t('Upload:')), folders(cfg.nas.upload)),
      h('div', { class: 'form-actions', style: 'margin:0' },
        h('button', { class: 'btn', type: 'button', onclick: async () => {
          nasNote.className = 'note';
          nasNote.textContent = t('Testing the connection…');
          try {
            await api('nas/test', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(cfg.nas) });
            nasNote.className = 'note good';
            nasNote.textContent = t('The connection works and writing succeeds.');
          } catch (err) {
            nasNote.className = 'note bad';
            nasNote.textContent = t('Does not work: {0}', tErr(err.message));
          }
        } }, t('Test connection')), nasNote)),
    section(t('Diagnostics'), t('For when something goes wrong. Off by default, because it writes more to the SD card.'),
      h('div', { class: 'checks' }, check(cfg, 'diagnostics', t('Keep diagnostics'),
        t('(keeps the status and system log, and at every start puts {0} on the CAM drive with what happened last time)', 'squirrel-log.txt'))),
      h('p', { class: 'muted', style: 'margin:0;font-size:14px' }, t('To read it: connect the Pi to your computer with the USB cable and open the CAM drive.'))),
    section(t('Access'), t('Enter a user and password to protect this page. Leave empty for open access on your network.'),
      text(cfg.web, 'user', t('User')),
      text(cfg.web, 'password', t('Password'), null, 'password')),
    h('div', { class: 'form-actions' }, h('button', { class: 'btn primary', type: 'submit' }, t('Save settings')), note),
  );
  app().replaceChildren(h('div', { class: 'page' }, h('h2', {}, t('Settings')), wifiSection(), form));
}

// ---------- wifi ----------

function wifiText(w) {
  switch (w.mode) {
    case 'client': return t('Connected to {0}', w.ssid);
    case 'hotspot': return t('Hotspot {0} is on, {1} connected', w.ssid, count(w.clients, 'device', 'devices'));
    case 'searching': return t('Looking for a known network');
    default: return w.error ? tErr(w.error) : t('Wifi is off');
  }
}

// Changes here apply right away; they are not part of the form below.
function wifiSection() {
  const status = h('p', { class: 'muted' }, t('Loading…'));
  const list = h('div', { class: 'checks' });
  const note = h('p', { class: 'note' });
  const say = (cls, text) => { note.className = 'note ' + cls; note.textContent = text; };
  const ssid = h('input', { type: 'text', list: 'wifi-seen', autocomplete: 'off' });
  const pw = h('input', { type: 'password', autocomplete: 'new-password' });
  const seen = h('datalist', { id: 'wifi-seen' });
  const hs = { enabled: h('input', { type: 'checkbox' }), ssid: h('input', { type: 'text' }), pw: h('input', { type: 'text', autocomplete: 'off' }) };

  const load = async () => {
    try {
      const d = await api('wifi');
      status.textContent = wifiText(d.status);
      list.replaceChildren(...(d.networks.length ? d.networks.map(n => h('div', { class: 'netrow' },
        h('span', {}, n.ssid, n.active ? h('small', {}, ' ' + t('connected')) : null),
        h('button', { class: 'btn', type: 'button', onclick: async () => {
          if (!confirm(n.active
            ? t('You are connected through {0}. After removing it this page goes away until the Pi uses another network or its hotspot. Continue?', n.ssid)
            : t('Remove {0}?', n.ssid))) return;
          try { await api('wifi/networks?uuid=' + encodeURIComponent(n.uuid), { method: 'DELETE' }); load(); } catch (e) { say('bad', tErr(e.message)); }
        } }, t('Remove')))) : [h('p', { class: 'muted' }, t('No networks yet.'))]));
      hs.enabled.checked = d.hotspot.ap;
      hs.ssid.value = d.hotspot.ap_ssid || '';
      hs.pw.value = d.hotspot.ap_password || '';
    } catch (e) { status.textContent = t('Wifi information not available: {0}', tErr(e.message)); }
  };
  load();

  return h('section', { class: 'section' },
    h('header', {}, h('h3', {}, 'Wifi'), h('p', {}, t('The networks the Pi connects to. Changes apply right away.'))),
    h('div', { class: 'fields' },
      status, list,
      h('label', { class: 'field' }, t('Add network'), ssid, seen,
        h('button', { class: 'btn', type: 'button', style: 'justify-self:start', onclick: async e => {
          e.target.disabled = true;
          try {
            const nets = await api('wifi/scan');
            seen.replaceChildren(...nets.map(n => h('option', { value: n.ssid }, `${n.signal}%`)));
            say('', nets.length ? t('Found {0}; pick one under Add network.', count(nets.length, 'network', 'networks')) : t('No networks found.'));
          } catch (err) { say('bad', t('Search failed: {0}', tErr(err.message))); }
          e.target.disabled = false;
        } }, t('Find networks'))),
      h('label', { class: 'field' }, t('Password'), pw, h('span', {}, t('Leave empty for an open network.'))),
      h('div', { class: 'form-actions', style: 'margin:0' },
        h('button', { class: 'btn', type: 'button', onclick: async () => {
          try {
            await api('wifi/networks', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ ssid: ssid.value.trim(), password: pw.value }) });
            say('good', t('{0} added.', ssid.value.trim()));
            ssid.value = pw.value = '';
            load();
          } catch (err) { say('bad', tErr(err.message)); }
        } }, t('Add network')), note),
      h('div', { class: 'checks' }, h('label', {}, hs.enabled, h('span', {}, t('Hotspot when no known network is in reach'),
        h('small', {}, ' ' + t('(to watch clips in the car or set up wifi)'))))),
      h('label', { class: 'field' }, t('Hotspot name'), hs.ssid),
      h('label', { class: 'field' }, t('Hotspot password'), hs.pw),
      h('div', { class: 'form-actions', style: 'margin:0' }, h('button', { class: 'btn', type: 'button', onclick: async () => {
        try {
          await api('wifi/hotspot', { method: 'PUT', headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ ap: hs.enabled.checked, ap_ssid: hs.ssid.value, ap_password: hs.pw.value }) });
          say('good', t('Hotspot settings saved.'));
        } catch (err) { say('bad', tErr(err.message)); }
      } }, t('Save hotspot')))));
}

// ---------- logs ----------

async function showLogs() {
  S.view = 'logs';
  const table = h('table', { class: 'logs' });
  app().replaceChildren(h('div', { class: 'page' }, h('h2', {}, t('Log')), h('p', { class: 'muted' }, t('The last 500 messages, newest first.')), table));
  const refresh = async () => {
    if (S.view !== 'logs') return;
    try {
      const recs = await api('logs');
      table.replaceChildren(...recs.reverse().map(r => h('tr', { class: 'lvl-' + r.level },
        h('td', {}, new Date(r.time).toLocaleString(LOCALE, { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit' })),
        h('td', {}, { INFO: '', WARN: t('Warning'), ERROR: t('Error'), DEBUG: 'Debug' }[r.level] ?? r.level),
        h('td', {}, tErr(r.msg)))));
    } catch {}
    setTimeout(refresh, 5000);
  };
  refresh();
}

// ---------- router ----------

function route() {
  const hash = location.hash.slice(1) || '/';
  const view = hash.startsWith('/clip/') || hash === '/' ? 'clips'
    : hash === '/status' ? 'status' : hash === '/settings' || hash === '/instellingen' ? 'settings' : hash === '/log' || hash === '/logboek' ? 'logs' : 'clips';
  for (const a of document.querySelectorAll('.tabs a')) {
    if (a.dataset.view === view) a.setAttribute('aria-current', 'page');
    else a.removeAttribute('aria-current');
  }
  if (view !== 'clips' && S.player) { S.player.destroy(); S.player = null; }
  if (view === 'clips') showClips(hash.startsWith('/clip/') ? decodeURIComponent(hash.slice(6)) : null);
  else if (view === 'status') showStatus();
  else if (view === 'settings') showSettings();
  else showLogs();
}

// The status every 2 seconds, each time a short request. A stream would
// keep one of the browser's six connections to the Pi open for good, and
// four cameras loading at once need the rest: with one short, a video can
// wait for a connection until it is sought.
async function connect() {
  for (;;) {
    try {
      const r = await fetch('api/status', { cache: 'no-store' });
      if (!r.ok) throw new Error(r.statusText);
      S.status = await r.json();
      onStatus();
    } catch {
      S.status = null;
      renderPulse();
    }
    await new Promise(r => setTimeout(r, 2000));
  }
}

window.addEventListener('hashchange', route);
translatePage();
renderPulse();
route();
connect();
