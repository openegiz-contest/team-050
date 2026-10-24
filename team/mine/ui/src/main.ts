import './style.css';

type Features = Record<string, {properties: {value: unknown; unit?: string}}>;
type Frame = {run_id: string; snapshot_seq: number; sim_time_s: number; entities: Record<string, Features>; received_at: string};
type Node = {id: string; name: string; kind: string; x: number; y: number};
type Topology = {topology_version: string; nodes: Node[]; edges: {from: string; to: string; length_km: number}[]; routes: {id: string; nodes: string[]}[]; shift_seconds: number; speed_loaded_kmh: number; speed_empty_kmh: number};
type Plan = {id: string; nodes: string[]};
type Preview = {plan: Plan; distance_km: number; loaded_minutes: number; empty_minutes: number; transition_path: string[]; route_revision: number; topology_version: string};
type CommandResult = {command_id: string; status: string; message: string; sim_time_s: number};

const labels: Record<string, string> = {waiting_service:'Ждёт сервисную машину', waiting_tow:'Ждёт эвакуатор', field_diagnosis:'Диагностика на месте', field_work:'Обслуживание на месте', hitching:'Сцепка с эвакуатором', being_towed:'На буксире', unhitching:'Отцепка', outbound:'Едет к самосвалу', towing:'Буксировка', returning_base:'Возвращается на базу', crew_away:'Бригада на выезде',idle:'Ожидает назначения', returning:'К погрузке', queued_loading:'Очередь на погрузку', positioning_loading:'Подъезд к экскаватору', loading:'Погрузка', hauling:'С грузом', queued_unloading:'Очередь на разгрузку', positioning_unloading:'Подъезд к дробилке', unloading:'Разгрузка', ready:'Подготовка', running:'Смена идёт', paused:'Пауза', finished:'Смена завершена', crushing:'Переработка', starved:'Нет подачи', received:'Отправка', scheduled:'Ожидает применения', applied:'Выполнено', rejected:'Отклонено', superseded:'Заменено', cancelled:'Отменено', failed:'Неисправен', to_repair:'Едет на ремонт', queued_repair:'Очередь ремонта', diagnosing:'Диагностика', repairing:'Ремонт'};
const $ = <T extends Element = HTMLElement>(id: string) => document.getElementById(id) as unknown as T;
const get = (f: Features | undefined, k: string, fallback: unknown = 0) => f?.[k]?.properties.value ?? fallback;
const n = (f: Features | undefined, k: string) => Number(get(f,k));
const str = (f: Features | undefined, k: string) => String(get(f,k,''));
const parse = <T>(value: unknown, fallback: T): T => {try {return JSON.parse(String(value)) as T ?? fallback;} catch {return fallback;}};
const fmt = (v: number, digits = 0) => new Intl.NumberFormat('ru-RU',{maximumFractionDigits:digits}).format(v);
const clock = (s: number) => `${String(Math.floor(s/3600)).padStart(2,'0')}:${String(Math.floor(s%3600/60)).padStart(2,'0')}:${String(Math.floor(s%60)).padStart(2,'0')}`;
let topology: Topology | null = null;
let frame: Frame | null = null;
let previous: Frame | null = null;
let frameAt = 0;
let selected = 'truck-01';
let role: 'dispatcher' | 'mechanic' = 'dispatcher';
let token = '';
let draft: string[] = [];
let drawing = false;
let preview: Preview | null = null;
let previewTarget = '';
let activeTab = 'equipment';
const commandLog: CommandResult[] = [];
let lastRun = '';
let noticeTimer: ReturnType<typeof setTimeout>;

$('app').innerHTML = `
<header class="topbar"><a class="brand" href="/" aria-label="Карьер — главная"><span class="brand-mark">К</span><span>КАРЬЕР<small>АКТОГАЙ · УЧЕБНЫЙ УЧАСТОК</small></span></a><div class="workspace-title"><span class="role-dot"></span> Рабочее место <select id="role" aria-label="Роль"><option value="dispatcher">Диспетчер</option><option value="mechanic">Механик</option></select><span class="version">03</span></div><div class="connection" id="connection">Подключение…</div></header>
<section class="shiftbar"><div class="shift-title"><span class="eyebrow">ТЕКУЩАЯ СМЕНА</span><strong id="shift-mode">Подготовка</strong></div><div class="clock"><b id="clock">00:00:00</b><span>/ 12:00:00</span><div class="progress"><i id="shift-progress"></i></div></div><div class="play-controls"><button id="start" class="primary">▶ Начать смену</button><button id="pause" disabled>Ⅱ Пауза</button><label class="speed-label">Скорость <select id="speed" aria-label="Скорость времени"><option value="1">×1</option><option value="10">×10</option><option value="30" selected>×30</option><option value="60">×60</option></select></label><button id="repeat" title="Новый прогон с теми же исходными назначениями">↻ Повтор</button></div></section>
<section class="metrics"><article><span>Переработано</span><strong id="processed">0 <small>т</small></strong><em>Зачёт сменного плана</em></article><article><span>Доставлено</span><strong id="delivered">0 <small>т</small></strong><em>Разгружено в бункеры</em></article><article><span>В пути / в кузовах</span><strong id="payload">0 <small>т</small></strong><em>Ещё не доставлено</em></article><article><span>В бункерах</span><strong id="buffers">0 <small>т</small></strong><em>Ожидает переработки</em></article><article class="norm"><span>Сменный норматив</span><strong id="plan-value">—</strong><em id="plan-progress">Ожидание данных из Ditto</em></article></section>
<div id="failure-banner" class="failure-banner" role="status" hidden></div><main class="workspace"><aside class="fleet panel"><div class="panel-heading"><h2>Самосвалы <span>20</span></h2></div><div class="fleet-tools"><button id="distribute" class="soft">Распределить по M1–M4</button><p>По 5 машин на каждый шаблон</p></div><div class="fleet-head"><span>Машина / состояние</span><span>Груз, т</span></div><div id="fleet-list" class="fleet-list"></div></aside>
<section class="map-panel panel"><div class="map-heading"><div><span class="eyebrow">ОПЕРАТИВНАЯ КАРТА</span><h1>Карьерный участок</h1></div><span class="map-scale">Схема · расстояния в км</span></div><div class="map-wrap"><svg id="map" viewBox="0 0 1140 910" role="img" aria-label="Карта карьера с дорогами и движущимися самосвалами"><defs><pattern id="grid" width="32" height="32" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="1" fill="#c6d2cc" opacity=".45"/></pattern></defs><rect width="1140" height="910" fill="url(#grid)"/><path d="M60 180 Q300 80 560 180 T1090 180 L1090 760 Q850 850 560 775 T60 760 Z" fill="none" stroke="#dce4dc" stroke-width="38" opacity=".4"/><g id="roads"></g><g id="route-highlight"></g><g id="nodes"></g><g id="trucks"></g></svg><div id="map-hint" class="map-hint">Выберите самосвал, чтобы увидеть его маршрут</div></div><div class="legend"><span><i class="loaded-dot"></i> С грузом</span><span><i class="empty-dot"></i> Пустой</span><span><i class="waiting-dot"></i> Ожидание / операция</span><span class="legend-note">С1/С2 — сервис · Э — эвакуатор</span></div></section>
<aside class="inspector panel"><div class="panel-heading"><h2>Выбранная машина</h2><span class="tag">CAT 793D</span></div><div class="truck-title"><span class="truck-illustration">▰</span><div><h2 id="truck-title">HT-01</h2><span id="truck-state">Ожидает назначения</span></div></div><div class="payload-meter"><div><span>Загрузка кузова</span><strong id="truck-payload">0 / 240 т</strong></div><div class="meter"><i id="payload-meter"></i></div></div><dl class="truck-details"><div><dt>Местоположение</dt><dd id="truck-position">Гараж P</dd></div><div><dt>Скорость</dt><dd id="truck-speed">0 км/ч</dd></div><div><dt>Завершено рейсов</dt><dd id="truck-trips">0</dd></div><div><dt>Ожидание в очередях</dt><dd id="truck-wait">0 мин</dd></div><div><dt>Текущий маршрут</dt><dd id="truck-route">Не назначен</dd></div></dl><div id="pending" class="pending" hidden></div><section id="maintenance" class="maintenance"><h3>Техническое состояние</h3><p id="maintenance-status">Исправен</p><p id="maintenance-detail"></p><label id="severity-label" for="failure-severity">Учебная поломка</label><select id="failure-severity"><option value="light">Лёгкая · на месте</option><option value="medium" selected>Средняя · своим ходом</option><option value="heavy">Тяжёлая · эвакуатор</option></select><button id="inject-failure" class="danger wide">Вызвать поломку</button><div id="mechanic-actions" hidden><label for="repair-site">Ремонтная площадка</label><select id="repair-site"><option value="">Ближайшая</option><option value="R1">R1 · одна бригада</option><option value="R2">R2 · одна бригада</option></select><label for="service-vehicle">Сервисная машина</label><select id="service-vehicle"><option value="">Ближайшая свободная</option><option value="service-01">СМ-01 · бригада R1</option><option value="service-02">СМ-02 · бригада R2</option></select><button id="send-service" class="primary wide">Выезд на лёгкую поломку</button><button id="preventive-service" class="wide">Профилактика на месте</button><button id="send-tow" class="wide">Вызвать эвакуатор</button><button id="send-repair" class="primary wide">Направить на ремонт</button><button id="prioritize-repair" class="wide">В начало очереди</button></div><p class="fine-print">Диагностика 5 мин. Лёгкая / профилактика 15 мин на месте; средняя 50 мин; тяжёлая 150 мин на площадке. На выезде бригада недоступна для стационарного ремонта. Самосвал ждёт бригаду на маршруте, груз сохраняется.</p></section><section class="route-editor" id="route-editor"><h3>Назначить маршрут</h3><label for="route-select">Готовый шаблон</label><select id="route-select"><option value="">Выберите маршрут</option></select><div class="route-actions"><button id="draw">⌁ Свой маршрут</button><button id="undo" title="Убрать последний узел">← Узел</button></div><div id="route-path" class="route-path">Выберите шаблон или узлы на карте</div><div id="route-preview" class="route-preview">Расстояние и время появятся после выбора пути.</div><button id="apply" class="primary wide" disabled>Назначить машине</button><p class="fine-print">Изменение в движении применяется на ближайшем допустимом узле.</p></section></aside></main>
<section class="bottom panel"><nav class="tabs" aria-label="Показатели смены"><button data-tab="equipment" class="active">Оборудование</button><button data-tab="events">Журнал событий</button><button data-tab="reports">Отчёты смен</button></nav><div id="equipment" class="tab-content"><div id="equipment-grid" class="equipment-grid"></div></div><div id="events" class="tab-content" hidden><div class="event-columns"><section><h3>Производственные события</h3><div id="event-list"></div></section><section><h3>Команды диспетчера</h3><div id="command-list"></div></section></div></div><div id="reports" class="tab-content" hidden><div class="report-heading"><p>Итог появится после 12 модельных часов. План учитывает переработанную руду.</p><button id="refresh-reports">Обновить</button></div><div id="report-list"></div></div></section><div id="notice" class="notice" role="status" hidden></div><footer>Условный участок по мотивам Актогая <span>20 самосвалов · 4 экскаватора · 3 дробилки</span></footer>`;

function notice(message: string, error = false) {clearTimeout(noticeTimer);$('notice').textContent=message;$('notice').className=`notice ${error?'error':''}`;$('notice').hidden=false;noticeTimer=setTimeout(()=>{$('notice').hidden=true;},6500);}
async function api<T>(path: string, body?: unknown): Promise<T> {const r=await fetch(path,{method:body===undefined?'GET':'POST',headers:body===undefined?{}:{'Content-Type':'application/json','X-Mine-Session':token},body:body===undefined?undefined:JSON.stringify(body)});const v=await r.json();if(!r.ok)throw new Error(v.error??`Ошибка ${r.status}`);return v as T;}
function command(type: string, parameters: Record<string,unknown> = {}, more: Record<string,unknown> = {}) {if(!frame)throw new Error('Состояние участка ещё загружается');return {schema_version:1,command_id:crypto.randomUUID(),run_id:frame.run_id,type,parameters,role,...more};}
async function send(type: string, parameters: Record<string,unknown> = {}, more: Record<string,unknown> = {}) {const cmd=command(type,parameters,more);const r=await api<{result:CommandResult}>('/api/commands',cmd);logCommand(r.result);return cmd.command_id;}
function act(action:()=>Promise<unknown>) {void action().catch(e=>notice(String(e.message??e),true));}
function logCommand(r: CommandResult){const at=commandLog.findIndex(v=>v.command_id===r.command_id);if(at>=0)commandLog[at]=r;else commandLog.unshift(r);if(commandLog.length>80)commandLog.pop();if(r.status==='rejected')notice(r.message,true);if(activeTab==='events')renderEvents();}
function node(id: string): Node {return topology!.nodes.find(n=>n.id===id)!;}
const svgNS='http://www.w3.org/2000/svg';
function el(name: string, attrs: Record<string,string|number>) {const e=document.createElementNS(svgNS,name);for(const [k,v]of Object.entries(attrs))e.setAttribute(k,String(v));return e;}
function textSVG(parent: Element,x: number,y: number,text: string,cls: string) {const e=el('text',{x,y,class:cls});e.textContent=text;parent.append(e);}
function drawTopology(){if(!topology)return;$('roads').replaceChildren();$('nodes').replaceChildren();for(const e of topology.edges){const a=node(e.from),b=node(e.to);const g=el('g',{});g.append(el('line',{x1:a.x,y1:a.y,x2:b.x,y2:b.y,class:'road-outline'}),el('line',{x1:a.x,y1:a.y,x2:b.x,y2:b.y,class:'road'}));textSVG(g,(a.x+b.x)/2,(a.y+b.y)/2-12,String(e.length_km).replace('.',','),'distance');$('roads').append(g);}
 for(const n of topology.nodes){const g=el('g',{class:`site-node ${n.kind}`,'data-node':n.id,tabindex:0,role:'button','aria-label':`${n.id} ${n.name}`});if(n.kind==='junction'){g.append(el('circle',{cx:n.x,cy:n.y,r:13}));textSVG(g,n.x,n.y-22,n.id,'junction-label');}else{g.append(el('rect',{x:n.x-55,y:n.y-28,width:110,height:56,rx:10}));textSVG(g,n.x,n.y+5,n.id,'node-id');textSVG(g,n.x,n.y+48,n.name,'node-name');if(n.kind==='garage')textSVG(g,n.x,n.y+69,'СТАРТ СМЕНЫ','node-subtitle');if(n.kind==='service')textSVG(g,n.x,n.y+69,'РЕМОНТ · 1 БРИГАДА','node-subtitle');}g.addEventListener('click',()=>pickNode(n.id));g.addEventListener('keydown',e=>{if((e as KeyboardEvent).key==='Enter')pickNode(n.id)});$('nodes').append(g);}
 const select=$<HTMLSelectElement>('route-select');for(const r of topology.routes){const o=document.createElement('option');o.value=r.id;o.textContent=`${r.id} · ${r.nodes[0]} → ${r.nodes.at(-1)}`;select.append(o);}renderHighlight();}
function pickNode(id:string){if(!drawing)return;if(!topology)return;if(draft.length===0){if(node(id).kind!=='loading'){notice('Начните с погрузки E1–E4');return}}else{if(draft.includes(id)){notice('Этот узел уже есть в пути');return};if(!topology.edges.some(e=>(e.from===draft.at(-1)&&e.to===id)||(e.to===draft.at(-1)&&e.from===id))){notice('Выберите соседний узел по дороге');return};if(node(draft.at(-1)!).kind==='crusher'){notice('Путь уже заканчивается на дробилке');return}}draft.push(id);draftChanged();}
function renderHighlight(){const g=$('route-highlight');g.replaceChildren();let nodes=draft;if(nodes.length===0&&frame){const p=parse<Plan|null>(get(frame.entities[selected],'route_json','null'),null);nodes=p?.nodes??[];}if(nodes.length<2)return;const points=nodes.map(id=>{const n=node(id);return `${n.x},${n.y}`}).join(' ');g.append(el('polyline',{points,class:'selected-route'}));}
async function updatePreview(){preview=null;($('apply') as HTMLButtonElement).disabled=true;if(!frame||!topology||draft.length<2||node(draft.at(-1)!).kind!=='crusher')return;const target=selected,path=JSON.stringify(draft);const p=await api<Preview>('/api/routes/preview',command('assign_route',{loaded_path:draft},{target_id:`org.openegiz.team050:${target}`}));if(target!==selected||path!==JSON.stringify(draft))return;preview=p;previewTarget=target;$('route-preview').textContent=`${fmt(p.distance_km,1)} км · с грузом ${fmt(p.loaded_minutes,1)} мин · обратно ${fmt(p.empty_minutes,1)} мин. Переход: ${p.transition_path.join(' → ')}. Время без очередей.`;($('apply') as HTMLButtonElement).disabled=false;}
function draftChanged(){preview=null;$('route-path').textContent=draft.length?draft.join(' → '):'Выберите погрузку E на карте';$('map-hint').textContent=drawing?'Постройте путь: погрузка E → соседние узлы → дробилка C':'Подсвечен выбранный маршрут';renderHighlight();act(updatePreview);}
function selectTruck(id:string){selected=id;draft=[];drawing=false;preview=null;($('apply') as HTMLButtonElement).disabled=true;($('route-select') as HTMLSelectElement).value='';$('route-path').textContent='Выберите шаблон или узлы на карте';$('route-preview').textContent='Расстояние и время появятся после выбора пути.';render();renderHighlight();}
const fleetRows = new Map<string, {button: HTMLButtonElement; subtitle: HTMLElement; weight: HTMLElement; dot: HTMLElement}>();
function renderFleet(){
 if(!frame)return;
 for(const id of Object.keys(frame.entities).filter(id=>id.startsWith('truck-')).sort()){
  const f=frame.entities[id],state=str(f,'state');
  let row=fleetRows.get(id);
  if(!row){
   const button=document.createElement('button'),dot=document.createElement('i'),label=document.createElement('span'),title=document.createElement('b'),subtitle=document.createElement('small'),weight=document.createElement('strong');
   title.textContent=`HT-${id.slice(-2)}`;label.append(title,subtitle);button.append(dot,label,weight);button.onclick=()=>selectTruck(id);$('fleet-list').append(button);
   row={button,subtitle,weight,dot};fleetRows.set(id,row);
  }
  row.button.className=`fleet-row ${id===selected?'selected':''}`;row.button.setAttribute('aria-pressed',String(id===selected));row.dot.className=`status-dot ${str(f,'failure_severity')?'broken':state==='hauling'?'loaded':state==='returning'?'empty':'waiting'}`;row.subtitle.textContent=labels[state]??state;row.weight.textContent=fmt(n(f,'payload_t'));
 }
}
function render(){if(!frame)return;const control=frame.entities['control-01'],mine=frame.entities['mine-01'],f=frame.entities[selected];const mode=str(control,'state');$('shift-mode').textContent=labels[mode]??mode;$('clock').textContent=clock(frame.sim_time_s);$('shift-progress').style.width=`${frame.sim_time_s/43200*100}%`;($('start') as HTMLButtonElement).disabled=mode==='running'||mode==='finished';$('start').textContent=mode==='paused'?'▶ Продолжить':'▶ Начать смену';($('pause') as HTMLButtonElement).disabled=mode!=='running';($('repeat') as HTMLButtonElement).disabled=mode==='running';($('speed') as HTMLSelectElement).value=String(n(control,'speedup'));
 for(const [id,key]of [['processed','processed_t'],['delivered','delivered_t'],['payload','truck_payload_total_t'],['buffers','buffer_total_t']]){$(id).replaceChildren(document.createTextNode(`${fmt(n(mine,key))} `));const u=document.createElement('small');u.textContent='т';$(id).append(u);}
 $('truck-title').textContent=`HT-${selected.slice(-2)}`;$('truck-state').textContent=labels[str(f,'state')]??str(f,'state');$('truck-payload').textContent=`${fmt(n(f,'payload_t'),1)} / 240 т`;$('payload-meter').style.width=`${n(f,'payload_t')/240*100}%`;$('truck-position').textContent=str(f,'edge_id').replace('>',' → ')||(str(f,'node_id')==='P'?'Гараж P':str(f,'node_id'));$('truck-speed').textContent=`${fmt(n(f,'speed_kmh'),1)} км/ч`;$('truck-trips').textContent=fmt(n(f,'trips_total'));$('truck-wait').textContent=`${fmt(n(f,'queue_wait_s')/60,1)} мин`;const route=parse<Plan|null>(get(f,'route_json'),null);$('truck-route').textContent=route?`${route.id} · ${route.nodes[0]} → ${route.nodes.at(-1)}`:'Не назначен';const pending=parse<Plan|null>(get(f,'pending_route_json'),null);$('pending').hidden=!pending;if(pending)$('pending').textContent=`Ожидает применения: ${pending.nodes.join(' → ')}`;
 $('plan-value').textContent=n(mine,'plan_t')>0?fmt(n(mine,'plan_t'))+' т':'—';$('plan-progress').textContent=n(mine,'plan_t')>0?fmt(n(mine,'plan_completion_pct'),1)+'% выполнено · диапазон 93–95 тыс. т':'План для архивного прогона не задан';
 renderFleet();renderEquipment();renderMaintenance();renderRepairSites();renderServiceVehicles();if(activeTab==='events')renderEvents();if(preview&&previewTarget===selected&&preview.route_revision!==n(f,'route_revision')){preview=null;($('apply') as HTMLButtonElement).disabled=true;$('route-preview').textContent='Назначение изменилось. Выберите путь повторно.';}renderHighlight();}
function renderEquipment(){if(!frame)return;const grid=$('equipment-grid');grid.replaceChildren();for(const id of ['excavator-01','excavator-02','excavator-03','excavator-04','crusher-01','crusher-02','crusher-03']){const f=frame.entities[id],crusher=id.startsWith('crusher');const card=document.createElement('article');card.className=`equipment-card ${crusher?'crusher-card':''}`;const h=document.createElement('h3');h.textContent=`${crusher?'Дробилка C':'Экскаватор E'}${Number(id.slice(-2))}`;const state=document.createElement('span');state.className='equipment-state';state.textContent=labels[str(f,'state')]??str(f,'state');const line=document.createElement('p');line.textContent=crusher?`${fmt(n(f,'buffer_t'))} / 720 т в бункере`:`${fmt(n(f,'loaded_t'))} т погружено`;const bar=document.createElement('div');bar.className='meter';const fill=document.createElement('i');fill.style.width=crusher?`${n(f,'buffer_t')/720*100}%`:(str(f,'state')==='loading'?'100%':'0');bar.append(fill);const detail=document.createElement('small');detail.textContent=`Очередь: ${n(f,'queue_length')}${crusher?` · мощность ${fmt(n(f,'capacity_tph'))} т/ч`:''}`;card.append(h,state,line,bar,detail);grid.append(card);}}
function appendLog(parent:Element,time:number,title:string,description?:string){const row=document.createElement('div');row.className='event-row';const t=document.createElement('time');t.textContent=clock(time);const text=document.createElement('span');text.textContent=title;if(description){const small=document.createElement('small');small.textContent=description;text.append(small)}row.append(t,text);parent.append(row);}
function renderEvents(){$('event-list').replaceChildren();$('command-list').replaceChildren();if(frame){const events=parse<{sim_time_s:number;text:string}[]>(get(frame.entities['mine-01'],'events_json'),[]);for(const e of [...events].reverse())appendLog($('event-list'),e.sim_time_s,e.text);}for(const r of commandLog)appendLog($('command-list'),r.sim_time_s??0,labels[r.status]??r.status,r.message);}
async function reports(){const runs=await api<Record<string,unknown>[]>('/api/runs');$('report-list').replaceChildren();for(const run of runs){const row=document.createElement('article');row.className='report-row';const title=document.createElement('strong');title.textContent=`Смена ${String(run.run_id).slice(0,8)} · ${clock(Number(run.sim_time_s))}`;const detail=document.createElement('span');detail.textContent=`Переработано ${fmt(Number(run.processed_t))} т · доставлено ${fmt(Number(run.delivered_t))} т`;const b=document.createElement('button');b.textContent='Скачать JSON';b.onclick=()=>{const url=URL.createObjectURL(new Blob([JSON.stringify(run,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download=`shift-${run.run_id}.json`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)};row.append(title,detail,b);$('report-list').append(row);}if(runs.length===0)$('report-list').textContent='Завершённых смен пока нет.';}

function pos(f:Features):{x:number;y:number}{const edge=str(f,'edge_id');if(edge){const [from,to]=edge.split('>'),a=node(from),b=node(to),p=n(f,'edge_progress');return{x:a.x+(b.x-a.x)*p,y:a.y+(b.y-a.y)*p};}const a=node(str(f,'node_id')||'P');return{x:a.x,y:a.y};}
let lastAnimation = 0;
const truckGroups = new Map<string, SVGGElement>();
function animate(now: number){
 if(now-lastAnimation<1000/30){requestAnimationFrame(animate);return;}lastAnimation=now;
 if(frame&&topology){
  const age=performance.now()-frameAt,live=age<5000;
  const connected=$('connection');connected.textContent=live?'● Участок на связи':'● Нет свежих данных';connected.classList.toggle('offline',!live);
  const g=$('trucks');
  for(const id of Object.keys(frame.entities).filter(id=>id.startsWith('truck-')||id.startsWith('service-')||id.startsWith('tow-')).sort()){
   const auxiliary=!id.startsWith('truck-');
   const f=frame.entities[id];let p=pos(f);
   const old=previous?.run_id===frame.run_id?previous.entities[id]:undefined;
   if(live&&old&&str(f,'edge_id')&&str(old,'edge_id')===str(f,'edge_id')&&str(frame.entities['control-01'],'state')==='running'&&n(f,'route_revision')===n(old,'route_revision')){
    const a=pos(old),alpha=Math.min(1,age/900);p={x:a.x+(p.x-a.x)*alpha,y:a.y+(p.y-a.y)*alpha};
   }
   const index=Number(id.slice(-2));
   if(!str(f,'edge_id')){p.x+=((index-1)%5-2)*15;p.y+=Math.floor((index-1)/5)*13-4;if(auxiliary)p.y+=id.startsWith('tow-')?40:24;}else{p.y+=(index%2?1:-1)*10;}
   let group=truckGroups.get(id);
   if(!group){
    group=el('g',{'data-truck':id,role:'button',tabindex:0,'aria-label':auxiliary?vehicleName(id):`Самосвал ${index}`}) as SVGGElement;
    group.append(el('circle',{cx:0,cy:0,r:19,class:'truck-ring'}),el('rect',{x:-12,y:-7,width:24,height:14,rx:4,class:'truck-body'}),el('rect',{x:-9,y:-5,width:13,height:10,rx:2,fill:'#ffffff',opacity:.35}));
    textSVG(group,0,3,id.startsWith('service-')?'С'+index:id.startsWith('tow-')?'Э':String(index).padStart(2,'0'),'truck-number');
    const title=el('title',{});title.textContent=auxiliary?vehicleName(id):`HT-${id.slice(-2)}`;group.append(title);
    group.addEventListener('click',e=>{e.stopPropagation();if(auxiliary)notice(vehicleName(id)+": "+(labels[str(frame?.entities[id],"state")]??str(frame?.entities[id],"state")));else selectTruck(id)});
    group.addEventListener('keydown',e=>{if(e.key==='Enter'){if(auxiliary)notice(vehicleName(id)+': '+(labels[str(frame?.entities[id],'state')]??str(frame?.entities[id],'state')));else selectTruck(id)}});
    g.append(group);truckGroups.set(id,group);
   }
   group.setAttribute('transform',`translate(${p.x} ${p.y})`);group.setAttribute('class',`truck ${id===selected?'chosen':''}`);
   group.querySelector('.truck-ring')!.setAttribute('visibility',id===selected?'visible':'hidden');
   const state=str(f,'state');group.querySelector('.truck-body')!.setAttribute('fill',auxiliary?(id.startsWith('tow-')?'#4154a2':'#26834e'):state==='failed'?'#c44842':state==='to_repair'?'#bf8435':str(f,'failure_severity')?'#8563a5':state==='hauling'?'#d99229':state==='returning'?'#378b9e':'#75878a');
  }
 }
 requestAnimationFrame(animate);
}

function renderMaintenance(){
 if(!frame)return;
 const f=frame.entities[selected],state=str(f,'state'),failure=str(f,'failure_severity'),mode=str(frame.entities['control-01'],'state');
 const ended=mode==='finished',started=mode!=='ready'&&!ended;
 const maintenance=Boolean(get(f,'maintenance',false));
 const severityNames:Record<string,string>={light:'Лёгкая',medium:'Средняя',heavy:'Тяжёлая'};
 $('maintenance-status').textContent=failure?`${severityNames[failure]??failure} неисправность · ${labels[state]??state}`:maintenance?`Профилактика · ${labels[state]??state}`:`Исправен · ремонтов: ${n(f,'repairs_completed')} · профилактик: ${n(f,'preventive_completed')}`;
 $('maintenance').classList.toggle('has-failure',!!failure);
 const site=str(f,'repair_node');
 $('maintenance-detail').textContent=failure||maintenance?`${site?site+' · ':''}Простой ${fmt(n(f,maintenance?'maintenance_downtime_s':'failure_downtime_s')/60,1)} мин · груз ${fmt(n(f,'payload_t'),1)} т${['diagnosing','repairing','field_diagnosis','field_work','hitching','unhitching'].includes(state)?` · осталось на этапе ${fmt(n(f,'operation_elapsed_s')/60,1)} мин`:''}${role==='dispatcher'&&state==='failed'?' · ожидает решения механика':''}`:'Поломки вызываются вручную. Профилактика учитывается отдельно; случайные отказы пока выключены.';
 for(const id of ['inject-failure','severity-label','failure-severity'])$(id).hidden=role!=='dispatcher';
 $<HTMLButtonElement>('inject-failure').disabled=!started||!!failure||maintenance;
 $('mechanic-actions').hidden=role!=='mechanic';
 $<HTMLButtonElement>('send-repair').disabled=!started||state!=='failed'||failure!=='medium';
 $<HTMLButtonElement>('send-service').disabled=!started||state!=='failed'||failure!=='light';
 $<HTMLButtonElement>('send-tow').disabled=!started||state!=='failed'||failure!=='heavy';
 $<HTMLButtonElement>('preventive-service').disabled=!started||!!failure||maintenance;
 $<HTMLButtonElement>('prioritize-repair').disabled=!started||state!=='queued_repair';
 $('route-editor').hidden=role!=='dispatcher';
 $<HTMLButtonElement>('distribute').disabled=role!=='dispatcher'||ended;
 if(role!=='dispatcher')for(const id of ['start','pause','repeat','speed'])($<HTMLButtonElement|HTMLSelectElement>(id)).disabled=true;
 const failed=n(frame.entities['mine-01'],'failed_trucks');
 $('failure-banner').hidden=failed===0;
 $('failure-banner').textContent=`Неисправных машин: ${failed}. Диспетчер перераспределяет исправную технику; механик направляет машины на ремонт.`;
}
function renderRepairSites(){
 if(!frame)return;
 for(const id of ['repair-01','repair-02']){
  const f=frame.entities[id];if(!f)continue;
  const site=`R${Number(id.slice(-2))}`,current=str(f,'current_truck_id'),queue=parse<string[]>(get(f,'queue_json','[]'),[]);
  const card=document.createElement('article');card.className='equipment-card repair-card';
  const title=document.createElement('h3');title.textContent=`Ремонт ${site} · 1 бригада`;
  const status=document.createElement('span');status.className='equipment-state';status.textContent=current?`${labels[str(f,'state')]??str(f,'state')} · HT-${current.slice(-2)}`:str(f,'state')==='crew_away'?'Бригада на выезде':'Бригада свободна';
  const detail=document.createElement('p');detail.textContent=`Завершено ремонтов: ${n(f,'repairs_completed')}`;
  const line=document.createElement('small');line.textContent=queue.length?`Очередь: ${queue.map(t=>'HT-'+t.slice(-2)).join(' → ')}`:'Очереди нет';
  card.append(title,status,detail,line);$('equipment-grid').append(card);
 }
}
$<HTMLSelectElement>('role').onchange=()=>{role=$<HTMLSelectElement>('role').value as 'dispatcher'|'mechanic';drawing=false;render();notice(role==='mechanic'?'Роль механика: выберите неисправную машину и ремонтную площадку':'Роль диспетчера: маршруты, время и вызов неисправности');};
$('inject-failure').onclick=()=>{const severity=$<HTMLSelectElement>('failure-severity').value;if(confirm(`Вызвать выбранную поломку HT-${selected.slice(-2)}? Машина остановится, груз сохранится.`))act(()=>send('inject_failure',{severity}, {target_id:`org.openegiz.team050:${selected}`}));};
 $('send-service').onclick=()=>act(()=>send('dispatch_service',{vehicle_id:$<HTMLSelectElement>('service-vehicle').value},{target_id:`org.openegiz.team050:${selected}`}));
 $('preventive-service').onclick=()=>act(()=>send('preventive_service',{vehicle_id:$<HTMLSelectElement>('service-vehicle').value},{target_id:`org.openegiz.team050:${selected}`}));
 $('send-tow').onclick=()=>act(()=>send('dispatch_tow',{repair_node:$<HTMLSelectElement>('repair-site').value},{target_id:`org.openegiz.team050:${selected}`}));
$('send-repair').onclick=()=>act(()=>send('send_to_repair',{repair_node:$<HTMLSelectElement>('repair-site').value},{target_id:`org.openegiz.team050:${selected}`}));
$('prioritize-repair').onclick=()=>act(()=>send('prioritize_repair',{}, {target_id:`org.openegiz.team050:${selected}`}));
$('start').onclick=()=>act(()=>send(frame&&str(frame.entities['control-01'],'state')==='paused'?'resume_shift':'start_shift'));
$('pause').onclick=()=>act(()=>send('pause_shift'));
$<HTMLSelectElement>('speed').onchange=()=>act(()=>send('set_speed',{speedup:Number($<HTMLSelectElement>('speed').value)}));
$('repeat').onclick=()=>{if(confirm('Повторить смену с исходными маршрутами, планом и поломками в те же моменты? Завершённые отчёты сохранятся.'))act(()=>send('repeat_shift'));};
$('draw').onclick=()=>{drawing=true;draft=[];$<HTMLSelectElement>('route-select').value='';draftChanged();};
$('undo').onclick=()=>{draft.pop();draftChanged();};
$<HTMLSelectElement>('route-select').onchange=()=>{drawing=false;const r=topology?.routes.find(r=>r.id===$<HTMLSelectElement>('route-select').value);draft=r?[...r.nodes]:[];draftChanged();};
$('apply').onclick=()=>act(async()=>{if(!preview||previewTarget!==selected)return;const p=preview;const routeID=$<HTMLSelectElement>('route-select').value;await send('assign_route',routeID?{route_id:routeID}:{loaded_path:p.plan.nodes},{target_id:`org.openegiz.team050:${selected}`,expected_route_revision:p.route_revision,topology_version:p.topology_version});preview=null;$<HTMLButtonElement>('apply').disabled=true;notice('Назначение отправлено диспетчером');});
$('distribute').onclick=()=>{if(!frame||!topology)return;if(!confirm('Назначить HT-01–05 → M1, HT-06–10 → M2, HT-11–15 → M3, HT-16–20 → M4? Машины выедут после начала смены.'))return;act(async()=>{const button=$<HTMLButtonElement>('distribute');button.disabled=true;try{for(let i=1;i<=20;i++){const id=`truck-${String(i).padStart(2,'0')}`;await send('assign_route',{route_id:`M${Math.floor((i-1)/5)+1}`},{target_id:`org.openegiz.team050:${id}`,expected_route_revision:n(frame!.entities[id],'route_revision'),topology_version:topology!.topology_version});}notice('20 назначений отправлены. Результаты — в журнале команд.');}finally{button.disabled=false;}});};
for(const b of document.querySelectorAll<HTMLButtonElement>('[data-tab]'))b.onclick=()=>{activeTab=b.dataset.tab!;for(const t of ['equipment','events','reports'])$(t).hidden=t!==activeTab;for(const button of document.querySelectorAll('[data-tab]'))button.classList.toggle('active',button===b);if(activeTab==='events')renderEvents();if(activeTab==='reports')act(reports);};
$('refresh-reports').onclick=()=>act(reports);
function receive(next:Frame){if(frame&&frame.run_id===next.run_id&&frame.snapshot_seq>=next.snapshot_seq)return;if(lastRun!==next.run_id){previous=null;draft=[];preview=null;lastRun=next.run_id;($('apply') as HTMLButtonElement).disabled=true;}else previous=frame;frame=next;frameAt=performance.now();if(topology)render();}
async function boot(){token=(await api<{token:string}>('/api/session')).token;const es=new EventSource('/api/stream');es.onmessage=e=>{try{const v=JSON.parse(e.data);if(v.type==='state')receive(v.data);if(v.type==='command')logCommand(v.data.result);}catch(err){console.error(err)}};es.onerror=()=>{$('connection').textContent='● Восстановление связи';};const load=async()=>{try{if(!topology){topology=await api<Topology>('/api/topology');drawTopology();}const v=await api<{frame:Frame|null;stale:boolean}>('/api/state');if(v.frame)receive(v.frame);if(frame&&topology)render();}catch{ /* the platform may still be starting */ }};await load();setInterval(()=>{if(!topology||!frame||performance.now()-frameAt>5000)void load();},3000);requestAnimationFrame(animate);}
void boot().catch(e=>notice(`Не удалось подключиться: ${e.message}`,true));

function vehicleName(id:string){return id.startsWith('tow-')?'Эвакуатор Э-01':`Сервисная СМ-${id.slice(-2)}`;}
function renderServiceVehicles(){
 if(!frame)return;
 for(const id of ['service-01','service-02','tow-01']){
 const f=frame.entities[id];if(!f)continue;
 const card=document.createElement('article');card.className='equipment-card service-card';
 const title=document.createElement('h3');title.textContent=vehicleName(id);
 const status=document.createElement('span');status.className='equipment-state';status.textContent=str(f,'state')==='idle'?'Свободна':labels[str(f,'state')]??str(f,'state');
 const target=str(f,'target_truck_id');const line=document.createElement('p');line.textContent=target?`HT-${target.slice(-2)} · ${str(f,'edge_id').replace('>',' → ')||str(f,'node_id')}`:`База ${str(f,'node_id')}`;
 const detail=document.createElement('small');detail.textContent=`Заданий: ${n(f,'jobs_completed')}${n(f,'operation_remaining_s')>0?' · этап: '+fmt(n(f,'operation_remaining_s')/60,1)+' мин':''}${id.startsWith('service-')?' · общая бригада с '+(id.endsWith('01')?'R1':'R2'):''}`;
 card.append(title,status,line,detail);$('equipment-grid').append(card);
 }
}
