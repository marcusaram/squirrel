'use strict';

// English is the source language; Dutch comes from the table below. A text
// without a translation shows in English, never as a blank.

const LANG = (() => {
  try {
    const l = localStorage.getItem('lang');
    if (l === 'en' || l === 'nl') return l;
  } catch {}
  const prefs = navigator.languages && navigator.languages.length ? navigator.languages : [navigator.language || ''];
  for (const p of prefs) {
    if (/^nl\b/i.test(p)) return 'nl';
    if (/^en\b/i.test(p)) return 'en';
  }
  return 'en';
})();
const LOCALE = LANG === 'nl' ? 'nl-NL' : 'en-GB';
document.documentElement.lang = LANG;

const NL = {
  'Map': 'Kaart',
  'Throttle': 'Gas',
  'Switch between km/h and mph': 'Wisselen tussen km/h en mph',
  'Brake': 'Rem',
  'Gear': 'Versnelling',
  'left': 'links',
  'right': 'rechts',
  'Left blinker': 'Richtingaanwijzer links',
  'Right blinker': 'Richtingaanwijzer rechts',
  'No driving data in this recording': 'Geen rijgegevens in deze opname',
  'Computer mode': 'Computermodus',
  'Switch to computer mode': 'Naar computermodus',
  'Back to car mode': 'Terug naar autostand',
  "Off: the USB drive is the car's.": 'Uit: de USB-schijf is van de auto.',
  'Preparing the drive… {0} of {1}': 'Schijf voorbereiden… {0} van {1}',
  'On: {0} files, {1}. Connect the Pi to your computer; it shows up as the drive Squirrel.': 'Aan: {0} bestanden, {1}. Sluit de Pi aan op je computer; hij verschijnt als de schijf Squirrel.',
  'Turns the Pi into a read-only USB drive for your computer, with every camera of every drive and event as one long video. Copying over USB is much faster than over wifi.':
    'Maakt van de Pi een alleen-lezen USB-schijf voor je computer, met elke camera van elke rit en gebeurtenis als één lange video. Kopiëren via USB gaat veel sneller dan via wifi.',
  'The car loses its USB drive until you switch back or restart the Pi. Do this with the Pi connected to a computer, not in the car. Continue?':
    'De auto heeft geen USB-schijf tot je terugschakelt of de Pi herstart. Doe dit met de Pi aan een computer, niet in de auto. Doorgaan?',
  'switch back to car mode first': 'schakel eerst terug naar de autostand',
  'switch back to car mode first; a computer may be reading these clips': 'schakel eerst terug naar de autostand; een computer leest deze clips misschien',
  'computer mode is still being prepared; wait for it to finish': 'de computermodus wordt nog voorbereid; wacht tot dat klaar is',
  'Download video': 'Video downloaden',
  'The chosen camera as one video': 'De gekozen camera als één video',
  'Self-driving': 'Zelfrijdend',
  'Autosteer': 'Autosteer',
  'Cruise': 'Cruise control',
  // time
  'today': 'vandaag',
  'yesterday': 'gisteren',
  '{0} h {1} min': '{0} u {1} min',
  'not yet': 'nog niet',
  'just now': 'zojuist',
  '{0} s ago': '{0} s geleden',
  '{0} min ago': '{0} min geleden',
  '{0} h ago': '{0} u geleden',
  'unknown date': 'onbekende datum',

  // counted things
  'file': 'bestand', 'files': 'bestanden',
  'event': 'gebeurtenis', 'events': 'gebeurtenissen',
  'drive': 'rit', 'drives': 'ritten',
  'group': 'groep', 'groups': 'groepen',
  'device': 'apparaat', 'devices': 'apparaten',
  'network': 'netwerk', 'networks': 'netwerken',

  // header and tabs
  'Main menu': 'Hoofdmenu',
  'Settings': 'Instellingen',
  'Log': 'Logboek',
  'Language': 'Taal',
  'USB off': 'USB uit',
  'Car is recording': 'Auto neemt op',
  'Connected to the car': 'Verbonden met auto',
  'Car is asleep': 'Auto slaapt',
  'Waiting for the car': 'Wacht op auto',
  'NAS: {0} to go': 'NAS: nog {0}',
  'NAS: {0} queued': 'NAS: {0} in wachtrij',
  'NAS up to date': 'NAS bijgewerkt',
  'NAS unreachable': 'NAS niet bereikbaar',
  'NAS error': 'NAS-fout',
  'NAS off': 'NAS uit',
  'No connection to the Pi': 'Geen verbinding met de Pi',
  'Last scan {0}': 'Laatste scan {0}',

  // clips
  'Drive': 'Rit',
  'Track mode': 'Circuitmodus',
  'Movement near the car': 'Beweging bij de auto',
  'Touch or bump': 'Aanraking of stoot',
  'Saved with the horn': 'Opgeslagen met claxon',
  'Saved from the dashcam': 'Opgeslagen via dashcam',
  'Saved by voice': 'Opgeslagen met spraak',
  'Saved clip': 'Opgeslagen clip',
  'All': 'Alles',
  'Saved': 'Opgeslagen',
  'Drives': 'Ritten',
  'Track': 'Circuit',
  'No clips yet': 'Nog geen clips',
  'Once the car records, drives and Sentry events show up here within a few minutes. This is where things stand:':
    'Zodra de auto opneemt, verschijnen ritten en Sentry-gebeurtenissen hier binnen een paar minuten. Zo staat het er nu voor:',
  'USB drive': 'USB-schijf',
  'Last scan': 'Laatste scan',
  'On the USB drive': 'Op de USB-schijf',
  'Kind': 'Soort',
  'Player': 'Speler',
  'Choose a clip': 'Kies een clip',
  'Sentry events, saved clips and drives are on the left, newest first.':
    'Sentry-gebeurtenissen, opgeslagen clips en ritten staan links, nieuwste eerst.',
  'Loading…': 'Laden…',
  'Clip not found': 'Clip niet gevonden',
  'It may just have been deleted to make room.': 'Hij is misschien net verwijderd om ruimte te maken.',
  'Back to the list': 'Terug naar de lijst',
  'Nothing in this category.': 'Niets in deze categorie.',
  'No clips kept yet': 'Nog geen clips bewaard',
  'Once the car records, drives and Sentry events show up here within a few minutes.':
    'Zodra de auto opneemt, verschijnen ritten en Sentry-gebeurtenissen hier binnen een paar minuten.',
  'on NAS': 'op NAS',

  // player
  'Front': 'Voor', 'Rear': 'Achter', 'Left': 'Links', 'Right': 'Rechts',
  'Left pillar': 'Links stijl', 'Right pillar': 'Rechts stijl',
  'Back': 'Terug',
  'Download all': 'Download alles',
  'Delete': 'Verwijderen',
  'No video': 'Geen beeld',
  'Play': 'Afspelen',
  'Pause': 'Pauzeren',
  'Back 10 seconds': '10 seconden terug',
  'Forward 10 seconds': '10 seconden vooruit',
  'Speed': 'Snelheid',
  'This group has no video.': 'Deze groep heeft geen video.',
  'Choose a camera': 'Kies een camera',
  'Timeline': 'Tijdlijn',
  'Moment of the event': 'Moment van de gebeurtenis',
  'All cameras': "Alle camera's",
  'One camera': 'Eén camera',
  'Delete this Sentry event?': 'Wil je deze Sentry-gebeurtenis verwijderen?',
  'Delete this saved clip?': 'Wil je deze opgeslagen clip verwijderen?',
  'Delete this drive?': 'Wil je deze rit verwijderen?',
  'Delete these track recordings?': 'Wil je deze circuitopnames verwijderen?',
  'Delete these clips?': 'Wil je deze clips verwijderen?',
  'The files are removed from the Pi and are not taken from the USB drive again.':
    'De bestanden gaan van de Pi af en worden niet opnieuw van de USB-schijf gehaald.',
  'Deleting failed: {0}': 'Verwijderen mislukt: {0}',

  // status
  'In use by the car': 'In gebruik door de auto',
  'Not connected': 'Niet aangesloten',
  'Connected': 'Aangesloten',
  'Powered, not recognised yet': 'Stroom, nog niet herkend',
  'Being recognised': 'Wordt herkend',
  'The USB drive is disconnected from the car for about a minute while its file system is checked. Do not do this while driving. Continue?':
    'De USB-schijf wordt ongeveer een minuut losgekoppeld van de auto terwijl het bestandssysteem wordt gecontroleerd. Niet doen tijdens het rijden. Doorgaan?',
  'Checking…': 'Bezig met controleren…',
  'Done, nothing to report.': 'Klaar, geen meldingen.',
  'Failed: {0}': 'Mislukt: {0}',
  'Check file system': 'Bestandssysteem controleren',
  'Car': 'Auto',
  'The USB drive the car sees.': 'De USB-schijf die de auto ziet.',
  'Storage': 'Opslag',
  'When space runs low, clips already on the NAS are cleared first, then the oldest drives.':
    'Bij te weinig ruimte worden eerst clips opgeruimd die al op de NAS staan, daarna de oudste ritten.',
  'Keeping clips': 'Bewaren',
  'Every finished clip is copied off the USB drive before the car throws it away.':
    'Elke voltooide clip wordt van de USB-schijf veiliggesteld voordat de auto hem weggooit.',
  'Scan now': 'Nu scannen',
  'Sync now': 'Nu synchroniseren',
  'System': 'Systeem',
  'Restart the Pi? The car will not see the USB drive for a moment.': 'De Pi herstarten? De auto ziet de USB-schijf dan even niet.',
  'Restart Pi': 'Pi herstarten',
  'Maintenance': 'Onderhoud',
  'Checks and repairs the file system of the USB drive. Use this when the car reports a problem with the USB drive.':
    'Controleert en herstelt het bestandssysteem van de USB-schijf. Gebruik dit als de auto meldt dat de USB-schijf een probleem heeft.',
  'Space for the car': 'Ruimte voor de auto',
  'Size not known yet.': 'Grootte nog onbekend.',
  'Now {0}, about {1}% of the card.': 'Nu {0}, ongeveer {1}% van de kaart.',
  'Now {0}, less than 1% of the card.': 'Nu {0}, minder dan 1% van de kaart.',
  "The car's USB drive is made again at {0}% of the card.": 'De USB-schijf voor de auto wordt opnieuw gemaakt op {0}% van de kaart.',
  'The Pi first keeps everything on it; then the drive is empty and gone from the car for about a minute. Do not do this while driving. Continue?':
    'Eerst bewaart de Pi alles wat erop staat; daarna is de schijf leeg en ongeveer een minuut weg bij de auto. Niet doen tijdens het rijden. Doorgaan?',
  'Keeping clips and making the new drive…': 'Clips bewaren en de nieuwe schijf maken…',
  'Make drive again': 'Schijf opnieuw maken',
  'How much of the card the car gets as its USB drive. The rest is for the clips the Pi keeps.':
    'Hoeveel van de kaart de auto als USB-schijf krijgt. De rest is voor clips die de Pi bewaart.',
  'New size': 'Nieuwe grootte',
  'Last write {0}': 'Laatst geschreven {0}',
  'Nothing written since the start': 'Nog niets geschreven sinds de start',
  '{0} free': '{0} vrij',
  'of {0}': 'van {0}',
  'Uploading is off': 'Uploaden staat uit',
  'Last success {0}': 'Laatst gelukt {0}',
  '{0} to go': '{0} te gaan',
  'Waiting for the Pi…': 'Wacht op de Pi…',
  'Unknown': 'Onbekend',
  'USB gadget is off': 'USB-gadget staat uit',
  'Writing': 'Schrijven',
  'last {0}': 'laatst {0}',
  'Reading': 'Lezen',
  'Drive image': 'Schijfimage',
  '{0}, {1} ({2} in use)': '{0}, {1} ({2} in gebruik)',
  '{0}% in use': '{0}% in gebruik',
  'Reserve limit': 'Grens van de reserve',
  'Free': 'Vrij',
  '{0} of {1}': '{0} van {1}',
  'Reserve': 'Reserve',
  'Kept': 'Bewaard',
  '{0} and {1}, {2}, {3}': '{0} en {1}, {2}, {3}',
  'Cleared': 'Opgeruimd',
  '{0}, last {1}': '{0}, laatst {1}',
  'Nothing yet': 'Nog niets',
  'Busy…': 'Bezig…',
  'Not yet': 'Nog niet',
  '{0}, clusters of {1}': '{0}, clusters van {1}',
  'not closed cleanly': 'niet netjes afgesloten',
  '{0}, of which {1} still recording or waiting': '{0}, waarvan {1} nog in opname of wachtend',
  'Kept since start': 'Bewaard sinds start',
  'Method': 'Methode',
  '{0} shared by reflink, {1} copied': '{0} gedeeld via reflink, {1} gekopieerd',
  'Last kept': 'Laatst bewaard',
  'Last error': 'Laatste fout',
  'Queue': 'Wachtrij',
  'Working on': 'Bezig met',
  '{0} ({1} of {2}, {3})': '{0} ({1} van {2}, {3})',
  'Last success': 'Laatst gelukt',
  'Device': 'Apparaat',
  'Network': 'Netwerk',
  'Wifi signal': 'Wifi-signaal',
  'Up for': 'Aan sinds',
  'Temperature': 'Temperatuur',
  'Load': 'Belasting',
  'Free memory': 'Geheugen vrij',
  'Version': 'Versie',

  // settings
  'Could not load the settings: {0}': 'Kon de instellingen niet laden: {0}',
  'Saved clips': 'Opgeslagen clips',
  '(everything recorded while driving; takes a lot of space)': '(alle opnames tijdens het rijden; neemt veel ruimte)',
  'Saving…': 'Opslaan…',
  'Saved.': 'Opgeslagen.',
  'Not saved: {0}': 'Niet opgeslagen: {0}',
  'Which clips the Pi copies off the USB drive. The car itself does not notice.':
    'Welke clips de Pi van de USB-schijf veiligstelt. De auto zelf merkt hier niets van.',
  'Scan interval in seconds': 'Scan-interval in seconden',
  'How often the USB drive is read. Keep it short: the car deletes what it recorded while parked as soon as you drive off.':
    'Hoe vaak de USB-schijf wordt gelezen. Houd het kort: de auto wist wat hij geparkeerd opnam zodra je wegrijdt.',
  'Keep free (GB)': 'Vrije ruimte reserveren (GB)',
  'Below this limit the Pi clears old clips, so the car can always keep writing.':
    'Onder deze grens ruimt de Pi oude clips op, zodat de auto altijd kan blijven schrijven.',
  'Uploads over SMB whenever the NAS is in reach, usually when the car is at home.':
    'Uploaden via SMB zodra de NAS bereikbaar is, meestal als de auto thuis staat.',
  'Upload to NAS': 'Uploaden naar NAS',
  'Name or IP address, for example nas.local or 192.168.1.20': 'Naam of IP-adres, bijvoorbeeld nas.local of 192.168.1.20',
  'Share': 'Gedeelde map',
  'The name of the share, for example video': 'De naam van de share, bijvoorbeeld video',
  'Folder in the share': 'Map binnen de share',
  'User': 'Gebruiker',
  'Password': 'Wachtwoord',
  'Domain': 'Domein',
  'Usually empty': 'Meestal leeg',
  'Upload:': 'Uploaden:',
  'Testing the connection…': 'Verbinding testen…',
  'The connection works and writing succeeds.': 'Verbinding werkt, schrijven lukt.',
  'Does not work: {0}': 'Werkt niet: {0}',
  'Test connection': 'Verbinding testen',
  'Diagnostics': 'Diagnose',
  'For when something goes wrong. Off by default, because it writes more to the SD card.':
    'Voor als er iets misgaat. Staat standaard uit, omdat het extra naar de SD-kaart schrijft.',
  'Keep diagnostics': 'Diagnose bijhouden',
  '(keeps the status and system log, and at every start puts {0} on the CAM drive with what happened last time)':
    '(houdt de status en de systeemlog bij, en zet bij elke start {0} op de USB-schijf CAM met wat er de vorige keer gebeurde)',
  'To read it: connect the Pi to your computer with the USB cable and open the CAM drive.':
    'Lezen: sluit de Pi met de USB-kabel aan op je computer en open de schijf CAM.',
  'Access': 'Toegang',
  'Enter a user and password to protect this page. Leave empty for open access on your network.':
    'Vul een gebruiker en wachtwoord in om deze pagina af te schermen. Leeg laten voor vrije toegang op je netwerk.',
  'Save settings': 'Instellingen opslaan',

  // wifi
  'Connected to {0}': 'Verbonden met {0}',
  'Hotspot {0} is on, {1} connected': 'Hotspot {0} staat aan, {1} verbonden',
  'Looking for a known network': 'Zoekt naar een bekend netwerk',
  'Wifi is off': 'Wifi staat uit',
  'connected': 'verbonden',
  'You are connected through {0}. After removing it this page goes away until the Pi uses another network or its hotspot. Continue?':
    'Je bent nu via {0} verbonden. Na verwijderen valt deze pagina weg, tot de Pi een ander netwerk of zijn hotspot gebruikt. Doorgaan?',
  'Remove {0}?': '{0} verwijderen?',
  'Remove': 'Verwijderen',
  'No networks yet.': 'Nog geen netwerken.',
  'Wifi information not available: {0}': 'Wifi-informatie niet beschikbaar: {0}',
  'The networks the Pi connects to. Changes apply right away.': 'De netwerken waar de Pi mee verbindt. Wijzigingen gaan direct in.',
  'Add network': 'Netwerk toevoegen',
  'Found {0}; pick one under Add network.': '{0} gevonden; kies er een bij Netwerk toevoegen.',
  'No networks found.': 'Geen netwerken gevonden.',
  'Search failed: {0}': 'Zoeken mislukt: {0}',
  'Find networks': 'Netwerken zoeken',
  'Leave empty for an open network.': 'Leeg laten voor een open netwerk.',
  '{0} added.': '{0} toegevoegd.',
  'Hotspot when no known network is in reach': 'Hotspot als er geen bekend netwerk is',
  '(to watch clips in the car or set up wifi)': '(om clips in de auto te bekijken of wifi in te stellen)',
  'Hotspot name': 'Naam van de hotspot',
  'Hotspot password': 'Wachtwoord van de hotspot',
  'Hotspot settings saved.': 'Hotspot-instellingen opgeslagen.',
  'Save hotspot': 'Hotspot opslaan',

  // log
  'The last 500 messages, newest first.': 'De laatste 500 meldingen, nieuwste bovenaan.',
  'Warning': 'Let op',
  'Error': 'Fout',

  // messages from the Pi (Go)
  'the network name must be 1 to 32 characters': 'de netwerknaam moet 1 tot 32 tekens hebben',
  'the password must be 8 to 63 characters': 'het wachtwoord moet 8 tot 63 tekens hebben',
  'network not found': 'netwerk niet gevonden',
  'not found': 'niet gevonden',
  'the scan interval must be between 2 and 30 seconds': 'het scan-interval moet tussen 2 en 30 seconden liggen',
  'the reserve must be at least 1 GB': 'de reserve moet minstens 1 GB zijn',
  'no wifi': 'geen wifi',
  'the hotspot needs a name (up to 32 characters) and a password of 8 to 63 characters':
    'de hotspot heeft een naam (max. 32 tekens) en een wachtwoord van 8 tot 63 tekens nodig',
  'Clips are copied instead of reflinked; use XFS for /backingfiles to save space and card wear.':
    'Clips worden gekopieerd in plaats van gereflinkt; gebruik XFS voor /backingfiles om ruimte en slijtage te sparen.',
  'little free space, and the kept clips still share their space with the USB drive or old snapshots; deleting them would not free anything':
    'weinig vrije ruimte, en de bewaarde clips delen hun ruimte nog met de USB-schijf of oude snapshots; verwijderen zou niets opleveren',
  'a repair is already running': 'er loopt al een reparatie',
  'choose between 10 and 70%': 'kies tussen 10 en 70%',
  'a maintenance task is already running': 'er loopt al een onderhoudstaak',
  'NetworkManager (nmcli) not found': 'NetworkManager (nmcli) niet gevonden',
  // log lines
  'starting': 'start',
  'archive indexed': 'archief ingelezen',
  'usb gadget ready': 'USB-schijf klaar voor de auto',
  'usb gadget': 'USB-schijf',
  'shutting down; the drive stays connected to the car': 'stopt; de schijf blijft verbonden met de auto',
  'clock restored from the last saved time': 'klok hersteld vanaf de laatst bewaarde tijd',
  'restoring the clock': 'klok herstellen',
  'added the TeslaCam folder to the drive': 'map TeslaCam op de schijf gezet',
  'adding TeslaCam folder': 'map TeslaCam aanmaken',
  'loading archive': 'archief laden',
  'archive walk': 'archief doorlopen',
  'web server': 'webserver',
  'writing diagnostics to the drive': 'diagnose naar de schijf schrijven',
  'repair: disconnecting drive from the car': 'reparatie: schijf losgekoppeld van de auto',
  'repair: drive reconnected': 'reparatie: schijf weer verbonden',
  'repair: reconnecting': 'reparatie: opnieuw verbinden',
  'repair finished': 'reparatie klaar',
  'resize: disconnecting drive from the car': 'grootte wijzigen: schijf losgekoppeld van de auto',
  'resize: reconnecting': 'grootte wijzigen: opnieuw verbinden',
  'resize: new drive for the car': 'grootte wijzigen: nieuwe schijf voor de auto',
  "cam image plus reserve is larger than the disk; the car's writes may fail when it fills up":
    'schijfimage plus reserve is groter dan de kaart; de auto kan vastlopen als die vol raakt',
};

// Messages from the Pi with something filled in.
const NL_PATTERNS = [
  [/^Keeping clips: (.*)$/s, (m, a) => 'Bewaren: ' + tErr(a)],
  [/^Storage: (.*)$/s, (m, a) => 'Opslag: ' + tErr(a)],
  [/^disconnecting failed, repair cancelled: (.*)$/s, 'loskoppelen mislukt, reparatie afgebroken: $1'],
  [/^disconnecting failed: (.*)$/s, 'loskoppelen mislukt: $1'],
  [/^keeping the clips failed; the drive was not changed: (.*)$/s, 'clips bewaren mislukt, de schijf is niet veranderd: $1'],
  [/^making the new drive failed; the old one stays: (.*)$/s, 'nieuwe schijf maken mislukt, de oude blijft: $1'],
  [/^(\d+) clips kept \((\d+) incomplete recordings skipped\)$/m, '$1 clips bewaard ($2 onvolledige opnames overgeslagen)'],
  [/^new USB drive of (\d+) GB$/m, 'nieuwe USB-schijf van $1 GB'],
];

function t(s, ...a) {
  let out = LANG === 'nl' && Object.prototype.hasOwnProperty.call(NL, s) ? NL[s] : s;
  if (a.length) out = out.replace(/\{(\d+)\}/g, (m, i) => (a[i] ?? m));
  return out;
}

// For text that comes from the Pi: whole messages first, then patterns
// (a pattern with the m flag applies to every line of a longer output).
function tErr(msg) {
  msg = String(msg ?? '');
  if (LANG !== 'nl' || !msg) return msg;
  if (Object.prototype.hasOwnProperty.call(NL, msg)) return NL[msg];
  // Log lines: a message followed by key=value details.
  const kv = msg.search(/ [\w.]+=/);
  if (kv > 0 && Object.prototype.hasOwnProperty.call(NL, msg.slice(0, kv))) return NL[msg.slice(0, kv)] + msg.slice(kv);
  for (const [re, rep] of NL_PATTERNS) {
    if (re.flags.includes('m')) msg = msg.replace(new RegExp(re.source, re.flags.includes('g') ? re.flags : re.flags + 'g'), rep);
    else if (re.test(msg)) return msg.replace(re, rep);
  }
  return msg;
}

// Static text in index.html carries data-t (text) or data-t-label (aria-label).
function translatePage() {
  for (const el of document.querySelectorAll('[data-t]')) el.textContent = t(el.dataset.t);
  for (const el of document.querySelectorAll('[data-t-label]')) el.setAttribute('aria-label', t(el.dataset.tLabel));
  const sw = document.getElementById('lang');
  if (!sw) return;
  sw.setAttribute('aria-label', t('Language'));
  for (const b of sw.querySelectorAll('button')) {
    b.setAttribute('aria-pressed', String(b.dataset.lang === LANG));
    b.addEventListener('click', () => {
      if (b.dataset.lang === LANG) return;
      try { localStorage.setItem('lang', b.dataset.lang); } catch {}
      location.reload();
    });
  }
}
