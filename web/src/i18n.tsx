// Translations for the main screens. Keys are the English text, so a missing
// translation simply shows English. French, Nigerian Pidgin, Yoruba, Igbo and
// Hausa were written for Ike and should be reviewed by native speakers before
// a release; send fixes as pull requests to web/src/i18n.tsx.
import { createContext, useContext, useEffect, useState, type ReactNode } from "react";

export type Lang = "en" | "fr" | "pcm" | "yo" | "ig" | "ha";

export const languages: { id: Lang; name: string; english: string }[] = [
  { id: "en", name: "English", english: "English" },
  { id: "fr", name: "Français", english: "French" },
  { id: "pcm", name: "Naijá", english: "Nigerian Pidgin" },
  { id: "yo", name: "Yorùbá", english: "Yoruba" },
  { id: "ig", name: "Igbo", english: "Igbo" },
  { id: "ha", name: "Hausa", english: "Hausa" },
];

type Dict = Record<string, string>;

const fr: Dict = {
  "Ike means strength.": "Ike signifie la force.", "Open your authenticator app and enter the code for": "Ouvrez votre application d'authentification et saisissez le code de",
  "Lost your phone? Enter one of your recovery codes instead.": "Téléphone perdu ? Saisissez plutôt un de vos codes de secours.",
  Dashboard: "Tableau de bord", Hosting: "Hébergement", Accounts: "Comptes", Websites: "Sites web", Email: "E-mail",
  "DNS zones": "Zones DNS", Databases: "Bases de données", Files: "Fichiers", Backups: "Sauvegardes", "Cron jobs": "Tâches cron",
  "App installer": "Installateur d'applications", Containers: "Conteneurs", "SSL certificates": "Certificats SSL", Terminal: "Terminal",
  Server: "Serveur", Servers: "Serveurs", Firewall: "Pare-feu", "Server software": "Logiciels du serveur", "PHP extensions": "Extensions PHP",
  "Resource usage": "Utilisation des ressources", Updates: "Mises à jour", "Security scan": "Analyse de sécurité", "Malware scanner": "Antivirus",
  "Migrate from cPanel": "Migrer depuis cPanel", Activity: "Activité", "AI agents": "Agents IA", Business: "Activité commerciale",
  "Hosting packages": "Offres d'hébergement", Billing: "Facturation", People: "Personnes", Alerts: "Alertes", Branding: "Image de marque",
  Settings: "Paramètres", Appearance: "Apparence", "Sign-in security": "Sécurité de connexion", "API tokens": "Jetons d'API",
  Notifications: "Notifications", Infrastructure: "Infrastructure", "Mail settings": "Réglages e-mail", "DNS settings": "Réglages DNS",
  Save: "Enregistrer", Cancel: "Annuler", Delete: "Supprimer", Add: "Ajouter", Create: "Créer", Edit: "Modifier", Close: "Fermer",
  Search: "Rechercher", Refresh: "Actualiser", "Sign in": "Se connecter", "Sign out": "Se déconnecter", Username: "Nom d'utilisateur",
  Password: "Mot de passe", Language: "Langue", Download: "Télécharger", Upload: "Envoyer", Restore: "Restaurer", "Run now": "Lancer maintenant",
  Help: "Aide", "Keyboard shortcuts": "Raccourcis clavier", "Mark all as read": "Tout marquer comme lu", "No notifications yet": "Aucune notification pour l'instant",
  "Type a command or search": "Tapez une commande ou recherchez", "Go to": "Aller à", Actions: "Actions", "No matches": "Aucun résultat",
  "Search websites, accounts, mailboxes": "Rechercher des sites, comptes, boîtes mail", Services: "Services", "Recent activity": "Activité récente",
  "Manage your servers, websites and email.": "Gérez vos serveurs, sites web et e-mails.", "Authentication code": "Code d'authentification",
  Verify: "Vérifier", "Use a different account": "Utiliser un autre compte", "Open-source hosting control for Linux servers.": "Panneau d'hébergement libre pour serveurs Linux.",
  "Accounts with two-factor sign-in are asked for a code next.": "Les comptes avec la double authentification demandent ensuite un code.",
  "Add website": "Ajouter un site", "Add account": "Ajouter un compte", "Add mailbox": "Ajouter une boîte mail", "Add zone": "Ajouter une zone",
  "New backup plan": "Nouveau plan de sauvegarde", "Switch theme": "Changer de thème", "Open help": "Ouvrir l'aide", "Unread": "Non lu",
  "Help for this page": "Aide pour cette page", "Read the documentation": "Lire la documentation", Everything: "Tout", Account: "Compte",
};

const pcm: Dict = {
  "Ike means strength.": "Ike mean strength.", "Open your authenticator app and enter the code for": "Open your authenticator app, put the code for",
  "Lost your phone? Enter one of your recovery codes instead.": "Your phone don lost? Put one of your recovery codes instead.",
  Dashboard: "Dashboard", Hosting: "Hosting", Accounts: "Akaunt dem", Websites: "Website dem", Email: "Email", "DNS zones": "DNS zone dem",
  Databases: "Database dem", Files: "File dem", Backups: "Backup dem", "Cron jobs": "Cron work dem", "App installer": "App installer",
  Containers: "Container dem", "SSL certificates": "SSL certificate dem", Terminal: "Terminal", Server: "Server", Servers: "Server dem",
  Firewall: "Firewall", "Server software": "Server software", "PHP extensions": "PHP extension dem", "Resource usage": "How resource dey go",
  Updates: "Update dem", "Security scan": "Check security", "Malware scanner": "Check for virus", "Migrate from cPanel": "Comot from cPanel come here",
  Activity: "Wetin don happen", "AI agents": "AI agent dem", Business: "Business", "Hosting packages": "Hosting package dem", Billing: "Bill and payment",
  People: "People", Alerts: "Alert dem", Branding: "Your brand", Settings: "Settings", Appearance: "How e go look", "Sign-in security": "Login security",
  "API tokens": "API token dem", Notifications: "Notification dem", Infrastructure: "Infrastructure", "Mail settings": "Email settings", "DNS settings": "DNS settings",
  Save: "Save am", Cancel: "Cancel", Delete: "Delete am", Add: "Add", Create: "Create", Edit: "Change am", Close: "Close", Search: "Find",
  Refresh: "Refresh", "Sign in": "Login", "Sign out": "Logout", Username: "Username", Password: "Password", Language: "Language",
  Download: "Download", Upload: "Upload", Restore: "Bring am back", "Run now": "Run am now", Help: "Help", "Keyboard shortcuts": "Keyboard shortcut dem",
  "Mark all as read": "Mark all as read", "No notifications yet": "No notification yet", "Type a command or search": "Type command or find something",
  "Go to": "Go", Actions: "Wetin you fit do", "No matches": "We no see am", "Search websites, accounts, mailboxes": "Find website, akaunt, mailbox",
  Services: "Service dem", "Recent activity": "Wetin happen recently", "Manage your servers, websites and email.": "Manage your server, website and email for one place.",
  "Authentication code": "Code from your app", Verify: "Confirm am", "Use a different account": "Use another akaunt",
  "Open-source hosting control for Linux servers.": "Open-source hosting control for Linux server.",
  "Accounts with two-factor sign-in are asked for a code next.": "If your akaunt get two-step login, we go ask for code next.",
  "Add website": "Add website", "Add account": "Add akaunt", "Add mailbox": "Add mailbox", "Add zone": "Add zone", "New backup plan": "New backup plan",
  "Switch theme": "Change theme", "Open help": "Open help", Unread: "You never read am", "Help for this page": "Help for this page",
  "Read the documentation": "Read the documentation", Everything: "Everything", Account: "Akaunt",
};

const yo: Dict = {
  Dashboard: "Pátákó àkọ́kọ́", Hosting: "Àlejò wẹ́ẹ̀bù", Accounts: "Àwọn àkáǹtì", Websites: "Àwọn ojú-òpó wẹ́ẹ̀bù", Email: "Ímeèlì",
  "DNS zones": "Àwọn agbègbè DNS", Databases: "Àwọn ibi ìpamọ́ dátà", Files: "Àwọn fáìlì", Backups: "Àwọn ẹ̀dà ìpamọ́", "Cron jobs": "Iṣẹ́ àtòjọ (cron)",
  "App installer": "Olùfisílẹ̀ áàpù", Containers: "Àwọn kọ́ntéènà", "SSL certificates": "Àwọn ìwé-ẹ̀rí SSL", Terminal: "Tẹ́mínà", Server: "Sáfà",
  Servers: "Àwọn sáfà", Firewall: "Ògiri ààbò", "Server software": "Sọ́fítíwià sáfà", "PHP extensions": "Àwọn àfikún PHP", "Resource usage": "Ìlò ohun-èlò",
  Updates: "Àwọn àtúnṣe", "Security scan": "Àyẹ̀wò ààbò", "Malware scanner": "Olùwádìí kòkòrò àìrí", "Migrate from cPanel": "Kó wá láti cPanel",
  Activity: "Ìṣẹ̀lẹ̀", "AI agents": "Àwọn aṣojú AI", Business: "Òwò", "Hosting packages": "Àwọn ìdìpọ̀ àlejò", Billing: "Ìsanwó", People: "Àwọn ènìyàn",
  Alerts: "Àwọn ìkìlọ̀", Branding: "Àmì ìdánimọ̀", Settings: "Ètò", Appearance: "Ìrísí", "Sign-in security": "Ààbò ìwọlé", "API tokens": "Àwọn kọ́kọ́rọ́ API",
  Notifications: "Àwọn ìfitónilétí", Infrastructure: "Ètò amáyédẹrùn", "Mail settings": "Ètò ímeèlì", "DNS settings": "Ètò DNS",
  Save: "Fi pamọ́", Cancel: "Fagilé", Delete: "Pa rẹ́", Add: "Fi kún", Create: "Ṣẹ̀dá", Edit: "Ṣàtúnṣe", Close: "Pa dé", Search: "Wá",
  Refresh: "Sọ dọ̀tun", "Sign in": "Wọlé", "Sign out": "Jáde", Username: "Orúkọ olùlò", Password: "Ọ̀rọ̀ aṣínà", Language: "Èdè",
  Download: "Gbà sílẹ̀", Upload: "Gbé sókè", Restore: "Dá padà", "Run now": "Ṣe é báyìí", Help: "Ìrànlọ́wọ́", "Keyboard shortcuts": "Àwọn ọ̀nà àbùjá kíbọ́ọ̀dù",
  "Mark all as read": "Sàmì sí gbogbo rẹ̀ pé o ti kà á", "No notifications yet": "Kò sí ìfitónilétí kankan síbẹ̀", "Type a command or search": "Tẹ àṣẹ tàbí wá nǹkan",
  "Go to": "Lọ sí", Actions: "Àwọn ìgbésẹ̀", "No matches": "Kò rí nǹkan", "Search websites, accounts, mailboxes": "Wá ojú-òpó, àkáǹtì, àpótí ímeèlì",
  Services: "Àwọn iṣẹ́", "Recent activity": "Ìṣẹ̀lẹ̀ àìpẹ́", "Manage your servers, websites and email.": "Ṣàkóso àwọn sáfà, ojú-òpó wẹ́ẹ̀bù àti ímeèlì rẹ.",
  "Authentication code": "Kóòdù ìjẹ́rìísí", Verify: "Jẹ́rìí sí i", "Use a different account": "Lo àkáǹtì mìíràn",
  "Open-source hosting control for Linux servers.": "Ìṣàkóso àlejò orísun-ṣíṣí fún àwọn sáfà Linux.",
  "Accounts with two-factor sign-in are asked for a code next.": "Àwọn àkáǹtì tó ní ìwọlé onígbésẹ̀ méjì yóò béèrè kóòdù tẹ̀lé e.",
  "Add website": "Fi ojú-òpó kún", "Add account": "Fi àkáǹtì kún", "Add mailbox": "Fi àpótí ímeèlì kún", "Add zone": "Fi agbègbè kún",
  "New backup plan": "Ètò ìpamọ́ tuntun", "Switch theme": "Yí àwọ̀ padà", "Open help": "Ṣí ìrànlọ́wọ́", Unread: "A kò tíì kà á",
  "Help for this page": "Ìrànlọ́wọ́ fún ojú-ewé yìí", "Read the documentation": "Ka ìwé àlàyé", Everything: "Gbogbo rẹ̀", Account: "Àkáǹtì",
};

const ig: Dict = {
  Dashboard: "Ihu mbụ", Hosting: "Nnabata weebụ", Accounts: "Akaụntụ", Websites: "Weebụsaịtị", Email: "Ozi-e", "DNS zones": "Mpaghara DNS",
  Databases: "Ebe nchekwa data", Files: "Faịlụ", Backups: "Ndabere", "Cron jobs": "Ọrụ cron", "App installer": "Onye nrụnye ngwa", Containers: "Kontena",
  "SSL certificates": "Asambodo SSL", Terminal: "Tamịnal", Server: "Sava", Servers: "Sava ndị a", Firewall: "Mgbidi nchekwa", "Server software": "Ngwanrọ sava",
  "PHP extensions": "Mgbakwunye PHP", "Resource usage": "Ojiji akụrụngwa", Updates: "Mmelite", "Security scan": "Nyocha nchekwa", "Malware scanner": "Nyocha nje kọmputa",
  "Migrate from cPanel": "Si na cPanel bịa", Activity: "Ihe mere", "AI agents": "Ndị ọrụ AI", Business: "Azụmahịa", "Hosting packages": "Ngwugwu nnabata",
  Billing: "Ịkwụ ụgwọ", People: "Ndị mmadụ", Alerts: "Ọkwa ịdọ aka na ntị", Branding: "Akara ika", Settings: "Ntọala", Appearance: "Ọdịdị",
  "Sign-in security": "Nchekwa nbanye", "API tokens": "Tokin API", Notifications: "Ọkwa", Infrastructure: "Ntọala nrụrụ ọrụ", "Mail settings": "Ntọala ozi-e",
  "DNS settings": "Ntọala DNS", Save: "Chekwaa", Cancel: "Kagbuo", Delete: "Hichapụ", Add: "Tinye", Create: "Mepụta", Edit: "Dezie", Close: "Mechie",
  Search: "Chọọ", Refresh: "Megharịa", "Sign in": "Banye", "Sign out": "Pụọ", Username: "Aha njirimara", Password: "Okwuntughe", Language: "Asụsụ",
  Download: "Budata", Upload: "Bulite", Restore: "Weghachi", "Run now": "Mee ya ugbu a", Help: "Enyemaka", "Keyboard shortcuts": "Ụzọ mkpirisi kiibọọdụ",
  "Mark all as read": "Kaa ha niile akara agụọla", "No notifications yet": "Enweghị ọkwa ọ bụla ugbu a", "Type a command or search": "Dee iwu ma ọ bụ chọọ ihe",
  "Go to": "Gaa na", Actions: "Ihe ị nwere ike ime", "No matches": "Ahụghị ihe ọ bụla", "Search websites, accounts, mailboxes": "Chọọ weebụsaịtị, akaụntụ, igbe ozi",
  Services: "Ọrụ", "Recent activity": "Ihe mere n'oge na-adịbeghị anya", "Manage your servers, websites and email.": "Jikwaa sava gị, weebụsaịtị gị na ozi-e gị.",
  "Authentication code": "Koodu nkwenye", Verify: "Kwenye", "Use a different account": "Jiri akaụntụ ọzọ",
  "Open-source hosting control for Linux servers.": "Njikwa nnabata mepere emepe maka sava Linux.",
  "Accounts with two-factor sign-in are asked for a code next.": "A ga-ajụ akaụntụ nwere nbanye nzọụkwụ abụọ koodu ọzọ.",
  "Add website": "Tinye weebụsaịtị", "Add account": "Tinye akaụntụ", "Add mailbox": "Tinye igbe ozi", "Add zone": "Tinye mpaghara",
  "New backup plan": "Atụmatụ ndabere ọhụrụ", "Switch theme": "Gbanwee agba", "Open help": "Mepee enyemaka", Unread: "Agụbeghị",
  "Help for this page": "Enyemaka maka peeji a", "Read the documentation": "Gụọ akwụkwọ nkọwa", Everything: "Ihe niile", Account: "Akaụntụ",
};

const ha: Dict = {
  Dashboard: "Allon farko", Hosting: "Masaukin yanar gizo", Accounts: "Asusu", Websites: "Shafukan yanar gizo", Email: "Imel", "DNS zones": "Yankunan DNS",
  Databases: "Rumbun bayanai", Files: "Fayiloli", Backups: "Ajiyar kwafi", "Cron jobs": "Ayyukan cron", "App installer": "Mai sanya manhajoji", Containers: "Kwantena",
  "SSL certificates": "Takardun shaidar SSL", Terminal: "Tashar umarni", Server: "Sabar", Servers: "Sabobi", Firewall: "Katangar tsaro", "Server software": "Manhajojin sabar",
  "PHP extensions": "Karin PHP", "Resource usage": "Amfani da albarkatu", Updates: "Sabuntawa", "Security scan": "Binciken tsaro", "Malware scanner": "Binciken kwayar cuta",
  "Migrate from cPanel": "Dawo daga cPanel", Activity: "Ayyuka", "AI agents": "Wakilan AI", Business: "Kasuwanci", "Hosting packages": "Fakitin masauki",
  Billing: "Biyan kudi", People: "Mutane", Alerts: "Gargadi", Branding: "Alamar kasuwanci", Settings: "Saituna", Appearance: "Kamanni",
  "Sign-in security": "Tsaron shiga", "API tokens": "Alamomin API", Notifications: "Sanarwa", Infrastructure: "Ababen more rayuwa", "Mail settings": "Saitunan imel",
  "DNS settings": "Saitunan DNS", Save: "Ajiye", Cancel: "Soke", Delete: "Goge", Add: "Kara", Create: "Kirkira", Edit: "Gyara", Close: "Rufe", Search: "Nema",
  Refresh: "Sabunta", "Sign in": "Shiga", "Sign out": "Fita", Username: "Sunan mai amfani", Password: "Kalmar sirri", Language: "Harshe",
  Download: "Sauke", Upload: "Dora", Restore: "Maido", "Run now": "Yi yanzu", Help: "Taimako", "Keyboard shortcuts": "Gajerun hanyoyin madannai",
  "Mark all as read": "Yi wa duka alamar an karanta", "No notifications yet": "Babu sanarwa tukuna", "Type a command or search": "Rubuta umarni ko nema",
  "Go to": "Je zuwa", Actions: "Abubuwan da za a yi", "No matches": "Ba a samu komai ba", "Search websites, accounts, mailboxes": "Nemi shafuka, asusu, akwatunan imel",
  Services: "Ayyuka", "Recent activity": "Abubuwan da suka faru kwanan nan", "Manage your servers, websites and email.": "Sarrafa sabobinka, shafukanka da imel dinka.",
  "Authentication code": "Lambar tantancewa", Verify: "Tabbatar", "Use a different account": "Yi amfani da wani asusu",
  "Open-source hosting control for Linux servers.": "Sarrafa masauki na budaddiyar manhaja don sabobin Linux.",
  "Accounts with two-factor sign-in are asked for a code next.": "Asusun da ke da shiga mataki biyu za a tambaye su lamba a gaba.",
  "Add website": "Kara shafi", "Add account": "Kara asusu", "Add mailbox": "Kara akwatin imel", "Add zone": "Kara yanki", "New backup plan": "Sabon tsarin ajiya",
  "Switch theme": "Canza jigo", "Open help": "Bude taimako", Unread: "Ba a karanta ba", "Help for this page": "Taimako don wannan shafin",
  "Read the documentation": "Karanta takardun bayani", Everything: "Komai", Account: "Asusu",
};

const dicts: Record<Lang, Dict> = { en: {}, fr, pcm, yo, ig, ha };

function initialLang(): Lang {
  try {
    const saved = localStorage.getItem("ike.lang") as Lang | null;
    if (saved && saved in dicts) return saved;
  } catch { /* storage blocked */ }
  const nav = (typeof navigator !== "undefined" ? navigator.language : "en").toLowerCase();
  if (nav.startsWith("fr")) return "fr";
  if (nav.startsWith("yo")) return "yo";
  if (nav.startsWith("ig")) return "ig";
  if (nav.startsWith("ha")) return "ha";
  if (nav.startsWith("pcm")) return "pcm";
  return "en";
}

let current: Lang = initialLang();

/** t translates outside React components (nav labels). */
export function t(s: string): string {
  return dicts[current][s] ?? s;
}

const Ctx = createContext<{ lang: Lang; setLang: (l: Lang) => void; t: (s: string) => string }>({ lang: current, setLang: () => {}, t });

export function I18nProvider({ children }: { children: ReactNode }) {
  const [lang, setState] = useState<Lang>(current);
  useEffect(() => {
    document.documentElement.lang = lang === "pcm" ? "pcm" : lang;
  }, [lang]);
  const setLang = (l: Lang) => {
    current = l;
    try { localStorage.setItem("ike.lang", l); } catch { /* ignore */ }
    setState(l);
  };
  return <Ctx.Provider value={{ lang, setLang, t: (s) => dicts[lang][s] ?? s }}>{children}</Ctx.Provider>;
}

export const useI18n = () => useContext(Ctx);
