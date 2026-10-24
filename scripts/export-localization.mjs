import fs from 'node:fs';
import path from 'node:path';
import crypto from 'node:crypto';
import cp from 'node:child_process';
import ts from '../team/mine/ui/node_modules/typescript/lib/typescript.js';

// Export current Russian product strings. No runtime files are modified.
const roots = process.argv.slice(2).length ? process.argv.slice(2) : ['.'];
const units = new Map();
const ru = /[А-Яа-яЁё]/;
const escape = s => String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
const normalize = s => s.replace(/\r\n/g,'\n');
const excluded = new Set(['node_modules','dist','.git','public']);
function files(dir) { if(!fs.existsSync(dir))return []; return fs.readdirSync(dir,{withFileTypes:true}).flatMap(e=>excluded.has(e.name)?[]:e.isDirectory()?files(path.join(dir,e.name)):[path.join(dir,e.name)]); }
function add(text,location,context,holders=[]) {
 text=normalize(text); if(!ru.test(text))return;
 const key=JSON.stringify([text,holders.map(h=>h.token)]);
 if(!units.has(key))units.set(key,{text,holders,locations:new Set(),contexts:new Set()});
 const u=units.get(key);u.locations.add(location);u.contexts.add(context);
}
function html(text,location,context,holders=[]) {
 const tags=/<[^>]*>|[^<]+/g;
 for(const m of text.matchAll(tags)) {
  if(m[0].startsWith('<')) {
   for(const a of m[0].matchAll(/\b(title|aria-label|alt|placeholder)\s*=\s*(["'])(.*?)\2/gs))add(a[3],location,`${context}; HTML ${a[1]}`,holders.filter(h=>a[3].includes(h.token)));
  } else if(ru.test(m[0])) {
   const part=m[0].replace(/^\s*\n\s*|\s*\n\s*$/g,'');
   add(part,location,`${context}; visible HTML text`,holders.filter(h=>part.includes(h.token)));
  }
 }
}
const revisions=[];
for(const input of roots) {
 const root=path.resolve(input);
 const revision=cp.execFileSync('git',['-C',root,'rev-parse','HEAD'],{encoding:'utf8'}).trim();
 const branch=cp.execFileSync('git',['-C',root,'rev-parse','--abbrev-ref','HEAD'],{encoding:'utf8'}).trim();
 const label=`${branch}@${revision.slice(0,7)}`;revisions.push(label);
 for(const file of files(path.join(root,'team/mine'))) {
  const rel=path.relative(root,file).replaceAll('\\','/'), source=fs.readFileSync(file,'utf8');
  if(/\.(ts|tsx|js)$/.test(file)) {
   const ast=ts.createSourceFile(file,source,ts.ScriptTarget.Latest,true);
   function visit(node) {
    let text=null,holders=[];
    if(ts.isStringLiteral(node)||ts.isNoSubstitutionTemplateLiteral(node))text=node.text;
    else if(ts.isTemplateExpression(node)) {
     text=node.head.text;
     node.templateSpans.forEach((span,i)=>{const token=`{value_${i+1}}`;holders.push({token,expression:span.expression.getText(ast)});text+=token+span.literal.text});
    }
    if(text!==null&&ru.test(text)) {
     const line=ast.getLineAndCharacterOfPosition(node.getStart(ast)).line+1;
     let parent=node.parent;while(parent&&!ts.isFunctionLike(parent)&&!ts.isVariableDeclaration(parent)&&!ts.isPropertyAssignment(parent))parent=parent.parent;
     const context=parent?.name?.getText(ast)||'UI string';
     const loc=`${label}:${rel}:${line}`;
     if(/<[a-zA-Z][\s>]/.test(text)||/<[a-zA-Z][^>]*>/.test(text))html(text,loc,context,holders);else add(text,loc,context,holders);
    }
    ts.forEachChild(node,visit);
   }
   visit(ast);
  } else if(file.endsWith('.go')) {
   // Match comments as well, so quoted examples in comments are not exported.
   for(const m of source.matchAll(/\/\/[^\n]*|\/\*[\s\S]*?\*\/|"(?:\\.|[^"\\])*"|`[^`]*`/g)) {
    if(m[0].startsWith('/'))continue;
    let text;try{text=m[0][0]==='`'?m[0].slice(1,-1):JSON.parse(m[0])}catch{continue}
    if(!ru.test(text))continue;
    const line=source.slice(0,m.index).split('\n').length;
    const prefix=source.slice(Math.max(0,m.index-160),m.index);
    const context=/Log\(|log\./.test(prefix)?'Event log / diagnostic':/reject|reply|result|error|message/i.test(prefix)?'Command result / validation / error':'Server text; may be a concatenated fragment';
    add(text,`${label}:${rel}:${line}`,context);
   }
  } else if(file.endsWith('.json')) {
   let value;try{value=JSON.parse(source)}catch{continue}
   const walk=(v,parts=[])=>{if(typeof v==='string')add(v,`${label}:${rel}#/${parts.join('/')}`,'Configuration label / dashboard text');else if(v&&typeof v==='object')for(const [k,x]of Object.entries(v))walk(x,[...parts,k]);};walk(value);
  } else if(file.endsWith('.html')) html(source,`${label}:${rel}`,'Document title / accessible label');
 }
}
const protectedPattern=/(\{value_\d+\}|%(?:\[[0-9]+\])?[-+#0 ]*(?:\d+|\*)?(?:\.(?:\d+|\*))?[a-zA-Z%]|https?:\/\/[^\s)<>]+|\b(?:HT-\d+|R[12]|E[1-4]|C[1-3]|J[1-4]|M[1-6]|CAT 793D|Hitachi EX5600-6|OpenEgiz|Ditto|MQTT|Grafana|Team 050)\b)/g;
function inline(text) {
 let out='',offset=0,id=0;
 for(const m of text.matchAll(protectedPattern)) {out+=escape(text.slice(offset,m.index));out+=`<ph id="${++id}" equiv="${escape(m[0])}" disp="${escape(m[0])}"/>`;offset=m.index+m[0].length;}
 return out+escape(text.slice(offset));
}
const notes=[
 'ЗАДАНИЕ ПЕРЕВОДЧИКУ: перевод русскоязычного интерфейса учебной симуляции карьера на английский (en). Заполните только target каждого сегмента; source, id, notes и защищённые ph не изменяйте. Сохраните UTF-8 и расширение .xlf. Это переводческий пакет, а не подключённый языковой модуль.',
 'Стиль: короткий, понятный язык производственного интерфейса. Роли: диспетчер — dispatcher; механик — mechanic; начальник смены / бригадир — shift supervisor. Предпочтительный вариант: international English; единицы СИ. Согласуйте все вхождения терминов.',
 'Глоссарий: карьер — open-pit mine; самосвал — haul truck; экскаватор — excavator; погрузка — loading; руда — ore; дробилка — crusher; бункер — hopper; ремонтная площадка — maintenance yard; бригада — maintenance crew; сервисная машина — service vehicle; эвакуатор — recovery vehicle; буксировка — towing; сцепка/отцепка — coupling/uncoupling; профилактика — preventive maintenance; смена — shift; сменный норматив — shift target; простой — downtime; резерв — reserve fleet; двойник — digital twin; Актогай — Aktogay.',
 'Тонны — metric tonnes (t), не short tons. м³ — m³; км — km; км/ч — km/h; т/ч — t/h; мин — min; с — s. План засчитывается по ПЕРЕРАБОТАННОЙ руде (processed), доставлено (delivered) и погружено (loaded) — разные показатели.',
 'ph — обязательные защищённые вставки. Перенесите каждую из source в target ровно один раз; можно менять порядок при необходимости. Не переводите выражения JavaScript, printf-спецификаторы, URL, протокольные идентификаторы, марки техники и коды узлов/машин. notes с value_N объясняют значения переменных; эти выражения не являются текстом перевода.',
 'Часть строк — фрагменты, соединяемые программой. Сохраняйте необходимые начальные/конечные пробелы, двоеточия, разделители и переносы. Не добавляйте HTML. Для динамического числа используйте форму, пригодную для любого значения (например, Repairs: {value_1}); вопросы о контексте вписывайте отдельной заметкой переводчика, не в target.',
 'Область: все найденные русские строковые литералы TypeScript/Go, HTML-тексты и доступные подписи, JSON-конфигурация и собственные панели Grafana в team/mine для перечисленных ревизий. Отдельно включены ошибки, результаты команд, журнал и серверная диагностика. Весь текст извлечён из исходников; одинаковые строки объединены с сохранением мест использования. Документация проекта, код сторонних библиотек/платформы и текст, запечённый в 3D-текстуры, не входят в программные строковые ресурсы.',
 'После перевода разработчик должен подключить каталог, переключение языка, en-локаль форматирования чисел/дат, формы множественного числа, переводы сообщений движка и проверку размещения длинных строк. Сам файл не переключает язык программы. Старые сохранённые журналы/архивные отчёты могут содержать ранее записанный русский текст.',
 `Исходные ревизии: ${revisions.join('; ')}. Сгенерировано ${new Date().toISOString().slice(0,10)}. Не переводите отметки времени и технические поля отчётов; переводятся их подписи в интерфейсе.`
];
let xml=`<?xml version="1.0" encoding="UTF-8"?>\n<xliff xmlns="urn:oasis:names:tc:xliff:document:2.0" version="2.0" srcLang="ru" trgLang="en">\n <file id="team050" original="team/mine">\n  <notes>\n${notes.map((n,i)=>`   <note id="brief-${i+1}" category="instructions">${escape(n)}</note>`).join('\n')}\n  </notes>\n`;
const records=[...units.values()].sort((a,b)=>a.text.localeCompare(b.text,'ru'));
for(const u of records) {
 const id='s_'+crypto.createHash('sha256').update(u.text).digest('hex').slice(0,16);
 const locations=[...u.locations].join('\n');
 const context=[...u.contexts].join('; ');
 const expressions=u.holders.map(h=>`${h.token} = ${h.expression}`).join('\n');
 xml+=`  <unit id="${id}">\n   <notes>\n    <note category="location">${escape(locations)}</note>\n    <note category="description">${escape(context)}</note>\n${expressions?`    <note category="variables">${escape(expressions)}</note>\n`:''}   </notes>\n   <segment state="initial">\n    <source xml:space="preserve">${inline(u.text)}</source>\n    <target xml:space="preserve"></target>\n   </segment>\n  </unit>\n`;
}
xml+=' </file>\n</xliff>\n';
const output=path.resolve('localization/TEAM050_ru-en.xlf');fs.mkdirSync(path.dirname(output),{recursive:true});fs.writeFileSync(output,xml,'utf8');
console.log(JSON.stringify({output,units:records.length,locations:records.reduce((n,u)=>n+u.locations.size,0),revisions},null,2));
