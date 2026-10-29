'use strict';

// English is the source language; NL holds the Dutch for each English
// text. Text without a translation shows in English.

const LANG = (() => {
  try {
    const l = localStorage.getItem('lang');
    if (l === 'en' || l === 'nl') return l;
  } catch (e) { /* no storage: follow the browser */ }
  const pref = (navigator.languages && navigator.languages[0]) || navigator.language || '';
  return pref.toLowerCase().startsWith('nl') ? 'nl' : 'en';
})();
document.documentElement.lang = LANG;

const NL = {
  // Page
  'Quit': 'Afsluiten',
  'Steps': 'Stappen',
  'Language': 'Taal',
  'An SD card for your Tesla Pi': 'Een SD-kaart voor je Tesla-Pi',
  'In seven steps you make a card that turns a Raspberry Pi or a Radxa ROCK 4C+ into a USB drive for the car, keeps everything the car records, and sends it to your NAS at home.':
    'In zeven stappen maak je een kaart waarmee een Raspberry Pi of een Radxa ROCK 4C+ zich bij de auto als USB-stick meldt, alles bewaart wat de auto opneemt en het thuis naar je NAS stuurt.',
  'Wifi': 'Wifi',
  'Board': 'Board',
  'Continue': 'Doorgaan',
  'Erase and write': 'Wissen en schrijven',
  'Already have a Squirrel card?': 'Heb je al een Squirrel-kaart?',
  'Update it': 'Bijwerken',
  'Update a card': 'Een kaart bijwerken',
  'Puts this version of Squirrel on a card that already has it. Settings, wifi networks and kept clips stay; the Pi installs the update at its next start, which takes a minute longer than usual.':
    'Zet deze versie van Squirrel op een kaart die het al heeft. Instellingen, wifi-netwerken en bewaarde clips blijven; de Pi installeert de update bij de volgende start, die daardoor een minuut langer duurt.',
  'Update card': 'Kaart bijwerken',
  'Make a new card instead': 'Toch een nieuwe kaart maken',
  'Looking for cards…': 'Kaarten zoeken…',
  '{0}; the version is read when updating': '{0}; de versie wordt bij het bijwerken gelezen',
  'Squirrel {0} for {1}': 'Squirrel {0} voor {1}',
  '(older version)': '(oudere versie)',
  'update not installed yet': 'update nog niet geïnstalleerd',
  'Squirrel {0} is put on the {1}. Settings and clips stay. Continue?': 'Squirrel {0} wordt op de {1} gezet. Instellingen en clips blijven. Doorgaan?',
  'Put the card back in the Pi and start it. The LED blinks fast for a minute while the update is installed.':
    'Zet de kaart terug in de Pi en start hem. De LED knippert een minuut snel terwijl de update wordt geïnstalleerd.',
  'this card has no Squirrel': 'deze kaart heeft geen Squirrel',
  'Updating the card': 'Kaart bijwerken',
  'Zero 2 W, 3, 4 or 5': 'Zero 2 W, 3, 4 of 5',
  'Car on the upper blue USB 3 port, power through USB-C': 'Auto aan de bovenste blauwe USB 3-poort, stroom via USB-C',
  'Works, slower NAS uploads (single core, 2.4 GHz wifi); Zero 2 W recommended. Not tested yet.':
    'Werkt, maar uploadt trager naar de NAS (één kern, wifi 2,4 GHz); Zero 2 W aanbevolen. Nog niet getest.',
  'Keep the recordings on a USB drive (SSD) instead of the card': 'Bewaar de opnames op een USB-schijf (SSD) in plaats van op de kaart',
  "For a Raspberry Pi 4 or 5 or a ROCK 4C+, on a USB port other than the car's. The space for the car is then taken from the drive. At the first start an empty drive is formatted; a drive with anything on it is left alone, and the first-start log says how to erase it. Some drives need more power than the car's USB port gives.":
    'Voor een Raspberry Pi 4 of 5 of een ROCK 4C+, op een andere USB-poort dan die naar de auto. De ruimte voor de auto komt dan ook van de schijf. Bij de eerste start wordt een lege schijf geformatteerd; een schijf waar iets op staat blijft ongemoeid, en het logboek van de eerste start zegt hoe je hem wist. Sommige schijven hebben meer stroom nodig dan de USB-poort van de auto geeft.',
  '{0}% of the USB drive': '{0}% van de USB-schijf',
  'Recordings kept on': 'Opnames bewaard op',
  'USB drive (formatted if empty)': 'USB-schijf (geformatteerd als hij leeg is)',
  'the card': 'de kaart',
  'this board has no USB port for a drive next to the one for the car': 'dit board heeft geen USB-poort voor een schijf naast die naar de auto',
  'This goes on the card. {0} ({1} MB) is downloaded once.': 'Dit komt op de kaart. {0} ({1} MB) wordt één keer gedownload.',
  'This goes on the card. {0} is downloaded once.': 'Dit komt op de kaart. {0} wordt één keer gedownload.',
  'Tested with Squirrel': 'Getest met Squirrel',
  'Not tested with Squirrel yet': 'Nog niet getest met Squirrel',
  'Not tested with Squirrel yet. It will probably work, but a change in the OS can break the first start. The tested image is the safe choice.':
    'Nog niet getest met Squirrel. Het werkt waarschijnlijk, maar een wijziging in het besturingssysteem kan de eerste start breken. Het geteste image is de veilige keuze.',
  'Newer releases could not be listed (no internet?); the tested one is offered.': 'Nieuwere versies konden niet worden opgehaald (geen internet?); het geteste image wordt aangeboden.',
  'Operating system': 'Besturingssysteem',
  'the chosen image is no longer offered; choose another': 'het gekozen image wordt niet meer aangeboden; kies een ander',
  'Checking the image': 'Image controleren',
  'this does not look like Armbian for the ROCK 4C+': 'dit lijkt geen Armbian voor de ROCK 4C+',
  "Where the Pi connects to: your wifi at home (for the NAS) and perhaps your phone's hotspot. You can change this later in the Pi's web interface.":
    'Waar de Pi mee verbindt: je wifi thuis (voor de NAS) en eventueel de hotspot van je telefoon. Later aanpassen kan in de web-interface van de Pi.',
  'Add another network': 'Nog een netwerk toevoegen',
  'Country': 'Land',
  'For the wifi channels allowed there.': 'Voor de toegestane wifi-kanalen.',
  "Password for the Pi's own hotspot": 'Wachtwoord van de eigen hotspot',
  'When the Pi sees no known network, it starts its own wifi called <b>Squirrel</b>. Use it to watch clips in the car.':
    'Ziet de Pi geen bekend netwerk, dan maakt hij zelf de wifi <b>Squirrel</b>. Daarmee bekijk je clips in de auto.',
  'Log in': 'Inloggen',
  'Logging in to the Pi': 'Inloggen op de Pi',
  'For maintenance over SSH. The web interface works without it.': 'Voor beheer via SSH. De web-interface werkt ook zonder.',
  'Name of the Pi': 'Naam van de Pi',
  'Reachable as': 'Bereikbaar als',
  'User': 'Gebruiker',
  'Password': 'Wachtwoord',
  'Show': 'Toon',
  'Hide': 'Verberg',
  'May be empty if you use an SSH key.': 'Mag leeg als je een SSH-sleutel gebruikt.',
  'SSH key': 'SSH-sleutel',
  "This computer's public key is already filled in.": 'De publieke sleutel van deze computer staat al ingevuld.',
  'Time zone': 'Tijdzone',
  'NAS and storage': 'NAS en opslag',
  'Once the car is home, the Pi uploads Sentry and saved clips to a shared folder on your NAS.':
    'Zodra de auto thuis staat, uploadt de Pi Sentry- en opgeslagen clips naar een gedeelde map op je NAS.',
  'Upload to a NAS (SMB)': 'Uploaden naar een NAS (SMB)',
  'Server': 'Server',
  'Shared folder': 'Gedeelde map',
  'Folder inside the share': 'Map binnen de share',
  "Space for the car's recordings": 'Ruimte voor de opnames van de auto',
  'The rest of the card is for clips the Pi keeps. 40% is a good start.':
    'De rest van de kaart is voor clips die de Pi bewaart. 40% is een goed begin.',
  'Build image': 'Image maken',
  'Build the image': 'Image maken',
  'SD card': 'SD-kaart',
  'Put it on the SD card': 'Op de SD-kaart zetten',
  'Insert the card in your computer. Everything on it is erased. The car writes all the time, so the card matters most: take an A2 or V30 card of 32 GB or more, or one sold for dashcams (High Endurance).':
    'Steek de kaart in je computer. Alles wat erop staat wordt gewist. De auto schrijft de hele tijd, dus de kaart doet er het meest toe: neem een A2- of V30-kaart van 32 GB of meer, of een kaart voor dashcams (High Endurance).',
  'Run the SD card faster (Zero W, Zero 2 W, Pi 3)': 'De SD-kaart sneller laten werken (Zero W, Zero 2 W, Pi 3)',
  'These boards drive the card at 50 MHz, about 20 MB/s: less than the car sends. This raises it to up to 100 MHz. That is beyond the SD card standard: a good A2 or V30 card usually copes, but a card that does not gives read and write errors and can lose recordings. Leave it off unless you want to try; Pi 4 and 5 do not need it.':
    'Deze boards sturen de kaart op 50 MHz aan, ongeveer 20 MB/s: minder dan de auto aanlevert. Dit verhoogt dat tot hooguit 100 MHz. Dat gaat buiten de SD-standaard: een goede A2- of V30-kaart kan het meestal aan, maar een kaart die het niet aankan geeft lees- en schrijffouten en kan opnames kwijtraken. Laat het uit tenzij je het wilt proberen; Pi 4 en 5 hebben het niet nodig.',
  'Up to 100 MHz (beyond the standard)': 'Tot 100 MHz (buiten de standaard)',
  'SD card overclocking is for a Raspberry Pi': 'het overklokken van de SD-kaart is voor een Raspberry Pi',
  'Write to card': 'Schrijf naar kaart',
  'Search again': 'Opnieuw zoeken',
  'Done': 'Klaar',
  'The card is ready': 'De kaart is klaar',
  '<b>Card in the Pi.</b> Connect the Pi to the car or a charger, using the USB data port.':
    '<b>Kaart in de Pi.</b> Sluit de Pi aan op de auto of een lader, met de USB-datapoort.',
  '<b>Wait a moment.</b> The first start takes a few minutes. The LED blinks fast while the Pi is busy.':
    '<b>Even wachten.</b> De eerste start duurt een paar minuten. De LED knippert snel zolang de Pi bezig is.',
  'Open it.': 'Openen.',
  'Go to': 'Ga naar',
  ', or join the wifi <b>Squirrel</b> if the Pi does not find your network.':
    ', of verbind met de wifi <b>Squirrel</b> als de Pi je netwerk niet vindt.',
  'Not working? Put the card in your computer: <code>squirrel/firstboot.log</code> tells where it stopped.':
    'Lukt het niet? Steek de kaart in je computer: <code>squirrel/firstboot.log</code> vertelt waar het stopte.',
  'Make another card': 'Nog een kaart maken',
  'Back': 'Vorige',
  'Next': 'Volgende',

  // Script
  'Network name': 'Netwerknaam',
  'Remove': 'Verwijderen',
  'enter the password again': 'wachtwoord opnieuw invullen',
  'Image ready.': 'Image klaar.',
  'Show in Finder': 'Toon in Finder',
  'Show in Explorer': 'Toon in Verkenner',
  'Not started': 'Niet gestart',
  'No SD card found. Insert one and choose Search again.': 'Geen SD-kaart gevonden. Steek er een in en kies Opnieuw zoeken.',
  'Card': 'Kaart',
  '{0} of {1}': '{0} van {1}',
  'card': 'kaart',
  'Looking for cards failed: {0}': 'Kaarten zoeken mislukt: {0}',
  'Everything on the {0} ({1}) is erased and replaced by the Squirrel image. Continue?':
    'Alles op de {0} ({1}) wordt gewist en vervangen door de Squirrel-image. Doorgaan?',
  'Enter at least one wifi network.': 'Vul minstens één wifi-netwerk in.',
  'The country is a two-letter code, for example NL.': 'Het land is een code van twee letters, bijvoorbeeld NL.',
  'The hotspot password must be 8 to 63 characters.': 'Het hotspot-wachtwoord moet 8 tot 63 tekens hebben.',
  'The password for {0} must be 8 to 63 characters.': 'Het wachtwoord van {0} moet 8 tot 63 tekens hebben.',
  'No password is filled in for {0}. The Pi treats that as an open network without a password. Is that right?':
    'Voor {0} is geen wachtwoord ingevuld. De Pi behandelt dat als een open netwerk zonder wachtwoord. Klopt dat?',
  'Enter the password.': 'Vul het wachtwoord in.',
  "The Pi's name may only contain lowercase letters, digits and hyphens.": 'De naam van de Pi mag alleen kleine letters, cijfers en streepjes bevatten.',
  'Choose a user name with lowercase letters and digits (not root).': 'Kies een gebruikersnaam met kleine letters en cijfers (niet root).',
  'Enter a password or an SSH key, or you cannot log in to the Pi.': 'Vul een wachtwoord of een SSH-sleutel in, anders kun je niet inloggen op de Pi.',
  'The password must be at least 8 characters.': 'Het wachtwoord moet minstens 8 tekens hebben.',
  "Enter the NAS's server and shared folder, or turn uploading off.": 'Vul de server en de gedeelde map van de NAS in, of zet uploaden uit.',
  'Build the image first.': 'Maak eerst de image.',
  'Write the image to a card first.': 'Schrijf eerst de image naar een kaart.',
  'Step {0}: {1}': 'Stap {0}: {1}',
  'open network': 'open netwerk',
  'The Pi': 'De Pi',
  '{0}.local, user {1}': '{0}.local, gebruiker {1}',
  'Log in with': 'Inloggen met',
  'password': 'wachtwoord',
  ' and ': ' en ',
  'NAS': 'NAS',
  'no upload': 'niet uploaden',
  'For the car': 'Voor de auto',
  '{0}% of the card': '{0}% van de kaart',
  'Squirrel Maker has quit': 'Squirrel Maker is afgesloten',
  'You can close this tab.': 'Je kunt dit tabblad sluiten.',
  'Base: {0}. squirrel-maker {1}.': 'Basis: {0}. squirrel-maker {1}.',
  ' Note: this version was built without squirreld; use make maker.': ' Let op: deze versie is gebouwd zonder squirreld; gebruik make maker.',
  'No access': 'Geen toegang',
  'Open the link that squirrel-maker shows in the terminal.': 'Open de link die squirrel-maker in de terminal toont.',

  // From the server: job steps and errors
  'Starting': 'Starten',
  'Downloading Raspberry Pi OS': 'Raspberry Pi OS downloaden',
  'Checking the downloaded image': 'Gedownloade image controleren',
  'Unpacking the image': 'Image uitpakken',
  'Adapting the image': 'Image aanpassen',
  'Preparing the SD card': 'SD-kaart voorbereiden',
  'Asking for permission': 'Toestemming vragen',
  'Writing the SD card': 'SD-kaart beschrijven',
  'Verifying': 'Controleren',
  'Ejecting the card': 'Kaart uitwerpen',
  'Finishing': 'Afronden',
  'a task is already running': 'er loopt al een taak',
  'build the image first': 'maak eerst de image',
  'wait until the current task is done': 'wacht tot de huidige taak klaar is',
  'the background server does not respond': 'de achtergrondserver reageert niet',
  'no access to the card (cancelled?)': 'geen toegang tot de kaart (geannuleerd?)',
  'authopen did not return the card': 'authopen gaf de kaart niet terug',
  'the card does not hold what was written; try another card': 'de kaart bevat na het schrijven niet wat er geschreven is; probeer een andere kaart',
  'no MBR partition table': 'geen MBR-partitietabel',
  'the name may only contain lowercase letters, digits and hyphens': 'de naam mag alleen kleine letters, cijfers en streepjes bevatten',
  'the user name may only contain lowercase letters, digits, _ and -': 'de gebruikersnaam mag alleen kleine letters, cijfers, _ en - bevatten',
  'enter a password or an SSH key, or you cannot log in to the Pi': 'vul een wachtwoord of een SSH-sleutel in, anders kun je niet inloggen op de Pi',
  'the password must be at least 8 characters': 'het wachtwoord moet minstens 8 tekens hebben',
  'the SSH key must be one line starting with ssh- or ecdsa-': 'de SSH-sleutel moet één regel zijn die begint met ssh- of ecdsa-',
  'the country must be a two-letter code, for example NL': 'het land moet een code van twee letters zijn, bijvoorbeeld NL',
  'invalid time zone': 'ongeldige tijdzone',
  'the hotspot password must be 8 to 63 characters': 'het hotspot-wachtwoord moet 8 tot 63 tekens hebben',
  'the space for the car must be between 10 and 70%': 'de ruimte voor de auto moet tussen 10 en 70% liggen',
  'a wifi name must be 1 to 32 characters': 'een wifi-naam moet 1 tot 32 tekens hebben',
  'writing cards is only supported on macOS for now': 'kaarten schrijven kan voorlopig alleen op macOS',
  'adapting the image is only supported on macOS for now': 'de image aanpassen kan voorlopig alleen op macOS',
  'writing to a card needs administrator rights; start Squirrel Maker as administrator':
    'naar een kaart schrijven vraagt beheerdersrechten; start Squirrel Maker als administrator',
};

// Server messages with details filled in.
const NL_PATTERNS = [
  [/^Done$/, 'Klaar'],
  [/^Done in (.+)$/, 'Klaar in $1'],
  [/^Downloading (.+)$/, '$1 downloaden'],
  [/^Updated to (.+)$/, 'Bijgewerkt naar $1'],
  [/^invalid disk (.+)$/, 'ongeldige schijf $1'],
  [/^(\S+) is not a whole disk$/, '$1 is geen hele schijf'],
  [/^(\S+) is an internal disk$/, '$1 is een interne schijf'],
  [/^(\S+) is not a physical disk$/, '$1 is geen fysieke schijf'],
  [/^(\S+) is not a card or USB disk \((.*)\)$/, '$1 is geen kaart of USB-schijf ($2)'],
  [/^(\S+) holds Windows$/, 'op $1 staat Windows'],
  [/^(\S+) is write-protected \(check the lock switch on the card\)$/, '$1 is beveiligd tegen schrijven (schuifje op de kaart?)'],
  [/^(\S+) is (\d+) GB; expected an SD card of 8 GB to 2 TB$/, '$1 is $2 GB; een SD-kaart van 8 GB tot 2 TB wordt verwacht'],
  [/^the image \((\d+) GB\) does not fit on (.+)$/, 'de image ($1 GB) past niet op $2'],
  [/^no access to the card: (.+)$/, 'geen toegang tot de kaart: $1'],
  [/^writing to (\S+): (.+)$/, 'schrijven naar $1: $2'],
  [/^reading back: (.+)$/, 'teruglezen: $1'],
  [/^the download is corrupt \(SHA-256 (\S+), expected (\S+)\)$/, 'de download klopt niet (SHA-256 $1, verwacht $2)'],
  [/^unexpected partition layout (.+)$/, 'onverwachte partitie-indeling $1'],
  [/^the password for (.+) must be 8 to 63 characters$/, 'het wachtwoord van $1 moet 8 tot 63 tekens hebben'],
];

// t translates an English text and fills in {0}, {1}, ...
function t(s, ...args) {
  let r = LANG === 'nl' && Object.prototype.hasOwnProperty.call(NL, s) ? NL[s] : s;
  args.forEach((a, i) => { r = r.split('{' + i + '}').join(String(a)); });
  return r;
}

// tErr translates a message from the server.
function tErr(msg) {
  if (LANG !== 'nl' || !msg) return msg;
  if (Object.prototype.hasOwnProperty.call(NL, msg)) return NL[msg];
  for (const [re, nl] of NL_PATTERNS) {
    if (re.test(msg)) return msg.replace(re, nl);
  }
  return msg;
}

// Translates the page's fixed text (elements marked data-t) and sets up
// the language switch.
function translatePage() {
  document.querySelectorAll('[data-t]').forEach(el => {
    const key = el.innerHTML.replace(/\s+/g, ' ').trim();
    if (LANG === 'nl' && Object.prototype.hasOwnProperty.call(NL, key)) el.innerHTML = NL[key];
  });
  document.querySelectorAll('[aria-label]').forEach(el => el.setAttribute('aria-label', t(el.getAttribute('aria-label'))));
  document.querySelectorAll('[data-lang]').forEach(b => {
    const on = b.dataset.lang === LANG;
    b.setAttribute('aria-pressed', on);
    b.onclick = () => {
      if (on) return;
      try { localStorage.setItem('lang', b.dataset.lang); } catch (e) { /* only for this visit */ }
      location.reload();
    };
  });
}
