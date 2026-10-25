import './style.css';
import {placeVehicles, groupVehicles, type VehicleTrack, type VehiclePlacement} from './map-position';
import type * as THREE from 'three';

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
const pendingCommands=new Map<string,number>();
let pollingCommands=false;
let lastRun = '';
let noticeTimer: ReturnType<typeof setTimeout>;

$('app').innerHTML = `
<header class="topbar"><a class="brand" href="/" aria-label="Карьер — главная"><span class="brand-mark">К</span><span>КАРЬЕР<small>АКТОГАЙ · УЧЕБНЫЙ УЧАСТОК</small></span></a><div class="workspace-title"><span class="role-dot"></span> Рабочее место <select id="role" aria-label="Роль"><option value="dispatcher">Диспетчер</option><option value="mechanic">Механик</option></select><span class="version">04</span></div><div class="connection" id="connection">Подключение…</div></header>
<section class="shiftbar"><div class="shift-title"><span class="eyebrow">ТЕКУЩАЯ СМЕНА</span><strong id="shift-mode">Подготовка</strong></div><div class="clock"><b id="clock">00:00:00</b><span>/ 12:00:00</span><div class="progress"><i id="shift-progress"></i></div></div><div class="play-controls"><button id="start" class="primary">▶ Начать смену</button><button id="pause" disabled>Ⅱ Пауза</button><label class="speed-label">Скорость <select id="speed" aria-label="Скорость времени"><option value="1">×1</option><option value="10">×10</option><option value="30" selected>×30</option><option value="60">×60</option></select></label><button id="repeat" title="Новый прогон с теми же исходными назначениями">↻ Повтор</button></div></section>
<section class="metrics"><article><span>Переработано</span><strong id="processed">0 <small>т</small></strong><em>Зачёт сменного плана</em></article><article><span>Доставлено</span><strong id="delivered">0 <small>т</small></strong><em>Разгружено в бункеры</em></article><article><span>В пути / в кузовах</span><strong id="payload">0 <small>т</small></strong><em>Ещё не доставлено</em></article><article><span>В бункерах</span><strong id="buffers">0 <small>т</small></strong><em>Ожидает переработки</em></article><article class="norm"><span>Сменный норматив</span><strong id="plan-value">—</strong><em id="plan-progress">Ожидание данных из Ditto</em></article></section>
<div id="failure-banner" class="failure-banner" role="status" hidden></div><main class="workspace"><aside class="fleet panel"><div class="panel-heading"><h2>Самосвалы <span>20</span></h2></div><div class="fleet-tools"><button id="distribute" class="soft">Распределить по M1–M4</button><p>По 5 машин на каждый шаблон</p></div><div class="fleet-head"><span>Машина / состояние</span><span>Груз, т</span></div><div id="fleet-list" class="fleet-list"></div></aside>
<section id="map-panel" class="map-panel panel"><div class="map-heading"><div><span class="eyebrow">ОПЕРАТИВНАЯ КАРТА</span><h1>Карьерный участок</h1></div><div class="map-controls"><span id="map-scale" class="map-scale">План · расстояния в км</span><div class="map-mode" role="group" aria-label="Вид карты"><button id="view-2d" class="active" aria-pressed="true">2D</button><button id="view-3d" aria-pressed="false">3D</button></div><div class="map-tools"><button id="map-time" hidden>Продолжить смену</button><button id="map-zoom-out" aria-label="Уменьшить карту" title="Уменьшить">−</button><button id="map-zoom-reset" title="Сбросить масштаб">100%</button><button id="map-zoom-in" aria-label="Увеличить карту" title="Увеличить">+</button><button id="model-retry" hidden>Повторить загрузку 3D</button><button id="map-expand" title="Развернуть карту">Развернуть</button></div></div></div><div class="map-wrap"><svg id="map" viewBox="0 0 1140 910" role="group" aria-label="Карта карьера с дорогами и движущимися самосвалами"><defs><pattern id="grid" width="32" height="32" patternUnits="userSpaceOnUse"><circle cx="1" cy="1" r="1" fill="#c6d2cc" opacity=".45"/></pattern></defs><rect width="1140" height="910" fill="url(#grid)"/><path d="M60 180 Q300 80 560 180 T1090 180 L1090 760 Q850 850 560 775 T60 760 Z" fill="none" stroke="#dce4dc" stroke-width="38" opacity=".4"/><g id="roads"></g><g id="route-highlight"></g><g id="nodes"></g><g id="trucks"></g></svg><svg id="map3d" viewBox="0 0 1140 910" role="group" aria-label="Условная трёхмерная аксонометрическая карта карьера" hidden><defs><linearGradient id="terrain-top" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#dfe8d9"/><stop offset="1" stop-color="#bccdbb"/></linearGradient><linearGradient id="terrain-side" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#a8b9a7"/><stop offset="1" stop-color="#84998b"/></linearGradient></defs><rect width="1140" height="910" fill="#edf2ec"/><g id="terrain3d"></g><g id="benches3d"></g><g id="roads3d"></g><g id="parking3d"></g><g id="route-highlight-3d"></g><g id="nodes3d"></g><g id="trucks3d"></g></svg><canvas id="map3d-models" aria-hidden="true" hidden></canvas><svg id="map-overlay" viewBox="0 0 1140 910" role="group" aria-label="Техника и технические состояния"></svg><div id="map-node-info" class="map-node-info" hidden><button id="map-node-close" aria-label="Закрыть сведения">×</button><strong id="map-node-title"></strong><span id="map-node-detail"></span></div><div id="map-hint" class="map-hint">Колесо мыши — масштаб · перетаскивайте карту для перемещения карты</div></div><div class="legend"><span><i class="loaded-dot"></i> С грузом</span><span><i class="empty-dot"></i> Пустой</span><span><i class="waiting-dot"></i> Ожидание / операция</span><span id="map-legend-note" class="legend-note">С1/С2 — сервис · Э — эвакуатор · Л/С/Т — поломка · ТО — профилактика</span></div></section>
<aside class="inspector panel"><div class="panel-heading"><h2>Выбранная машина</h2><span class="tag">CAT 793D</span></div><div class="truck-title"><span class="truck-illustration">▰</span><div><h2 id="truck-title">HT-01</h2><span id="truck-state">Ожидает назначения</span></div></div><div class="payload-meter"><div><span>Загрузка кузова</span><strong id="truck-payload">0 / 240 т</strong></div><div class="meter"><i id="payload-meter"></i></div></div><dl class="truck-details"><div><dt>Местоположение</dt><dd id="truck-position">Гараж P</dd></div><div><dt>Скорость</dt><dd id="truck-speed">0 км/ч</dd></div><div><dt>Завершено рейсов</dt><dd id="truck-trips">0</dd></div><div><dt>Ожидание в очередях</dt><dd id="truck-wait">0 мин</dd></div><div><dt>Текущий маршрут</dt><dd id="truck-route">Не назначен</dd></div></dl><div id="pending" class="pending" hidden></div><section id="maintenance" class="maintenance"><h3>Техническое состояние</h3><p id="maintenance-status">Исправен</p><p id="maintenance-detail"></p><label id="severity-label" for="failure-severity">Учебная поломка</label><select id="failure-severity"><option value="light">Лёгкая · на месте</option><option value="medium" selected>Средняя · своим ходом</option><option value="heavy">Тяжёлая · эвакуатор</option></select><button id="inject-failure" class="danger wide">Вызвать поломку</button><div id="mechanic-actions" hidden><label for="repair-site">Ремонтная площадка</label><select id="repair-site"><option value="">Ближайшая</option><option value="R1">R1 · одна бригада</option><option value="R2">R2 · одна бригада</option></select><label for="service-vehicle">Сервисная машина</label><select id="service-vehicle"><option value="">Ближайшая свободная</option><option value="service-01">СМ-01 · бригада R1</option><option value="service-02">СМ-02 · бригада R2</option></select><button id="send-service" class="primary wide">Выезд на лёгкую поломку</button><button id="preventive-service" class="wide">Профилактика на месте</button><button id="send-tow" class="wide">Вызвать эвакуатор</button><button id="send-repair" class="primary wide">Направить на ремонт</button><button id="prioritize-repair" class="wide">В начало очереди</button></div><p class="fine-print">Диагностика 5 мин. Лёгкая / профилактика 15 мин на месте; средняя 50 мин; тяжёлая 150 мин на площадке. На выезде бригада недоступна для стационарного ремонта. Самосвал ждёт бригаду на маршруте, груз сохраняется.</p></section><section class="route-editor" id="route-editor"><h3>Назначить маршрут</h3><label for="route-select">Готовый шаблон</label><select id="route-select"><option value="">Выберите маршрут</option></select><div class="route-actions"><button id="draw">⌁ Свой маршрут</button><button id="undo" title="Убрать последний узел">← Узел</button></div><div id="route-path" class="route-path">Выберите шаблон или узлы на карте</div><div id="route-preview" class="route-preview">Расстояние и время появятся после выбора пути.</div><button id="apply" class="primary wide" disabled>Назначить машине</button><p class="fine-print">Изменение в движении применяется на ближайшем допустимом узле.</p></section></aside></main>
<section class="bottom panel"><nav class="tabs" aria-label="Показатели смены"><button data-tab="equipment" class="active">Оборудование</button><button data-tab="events">Журнал событий</button><button data-tab="reports">Отчёты смен</button></nav><div id="equipment" class="tab-content"><div id="equipment-grid" class="equipment-grid"></div></div><div id="events" class="tab-content" hidden><div class="event-columns"><section><h3>Производственные события</h3><div id="event-list"></div></section><section><h3>Команды диспетчера</h3><div id="command-list"></div></section></div></div><div id="reports" class="tab-content" hidden><div class="report-heading"><p>Итог появится после 12 модельных часов. План учитывает переработанную руду.</p><button id="refresh-reports">Обновить</button></div><div id="report-list"></div></div></section><div id="notice" class="notice" role="status" hidden></div><footer>Условный участок по мотивам Актогая <span>20 самосвалов · 4 экскаватора · 3 дробилки</span></footer>`;

function notice(message: string, error = false) {clearTimeout(noticeTimer);$('notice').textContent=message;$('notice').className=`notice ${error?'error':''}`;$('notice').hidden=false;noticeTimer=setTimeout(()=>{$('notice').hidden=true;},6500);}
async function api<T>(path:string,body?:unknown,recovered=false):Promise<T>{
 let response:Response;
 try{
  response=await fetch(path,{method:body===undefined?'GET':'POST',headers:body===undefined?{}:{'Content-Type':'application/json','X-Mine-Session':token},body:body===undefined?undefined:JSON.stringify(body),signal:AbortSignal.timeout(20000)});
 }catch{throw new Error('Сервер не отвечает. Проверьте запуск стенда и связь. Команды можно проверить в журнале.');}
 // A second browser tab can refresh the shared cookie. Retry the same command
 // identity after refreshing this tab's token; the backend remains idempotent.
 if(response.status===403&&body!==undefined&&!recovered){token=(await api<{token:string}>('/api/session')).token;return api<T>(path,body,true);}
 let value:unknown;
 try{value=await response.json()}catch{throw new Error(`Сервер вернул некорректный ответ (${response.status})`)}
 if(!response.ok)throw new Error((value as {error?:string}).error??`Ошибка ${response.status}`);
 return value as T;
}
function command(type: string, parameters: Record<string,unknown> = {}, more: Record<string,unknown> = {}) {if(!frame)throw new Error('Состояние участка ещё загружается');return {schema_version:1,command_id:crypto.randomUUID(),run_id:frame.run_id,type,parameters,role,...more};}
async function send(type: string, parameters: Record<string,unknown> = {}, more: Record<string,unknown> = {}) {const cmd=command(type,parameters,more);const r=await api<{result:CommandResult}>('/api/commands',cmd);logCommand(r.result);if(r.result.status==='received')notice('Команда отправлена. Подтверждение — в журнале команд.');return cmd.command_id;}
function act(action:()=>Promise<unknown>) {void action().catch(e=>notice(String(e.message??e),true));}
function logCommand(r: CommandResult){if(r.status==='received'||r.status==='scheduled'){if(!pendingCommands.has(r.command_id))pendingCommands.set(r.command_id,Date.now())}else pendingCommands.delete(r.command_id);const at=commandLog.findIndex(v=>v.command_id===r.command_id);if(at>=0)commandLog[at]=r;else commandLog.unshift(r);if(commandLog.length>80)commandLog.pop();if(r.status==='rejected')notice(r.message,true);if(activeTab==='events')renderEvents();}
function node(id: string): Node {return topology!.nodes.find(n=>n.id===id)!;}
const svgNS='http://www.w3.org/2000/svg';
function el(name: string, attrs: Record<string,string|number>) {const e=document.createElementNS(svgNS,name);for(const [k,v]of Object.entries(attrs))e.setAttribute(k,String(v));return e;}
function textSVG(parent: Element,x: number,y: number,text: string,cls: string) {const e=el('text',{x,y,class:cls});e.textContent=text;parent.append(e);}
type Point = {x:number;y:number};
let mapView:'2d'|'3d'='2d';
let selectedMapNode='';
const mapBounds={x:0,y:0,width:1140,height:910};
let mapBox={...mapBounds};
let mapDrag:{pointerId:number;x:number;y:number;box:typeof mapBox;svg:SVGSVGElement;moved:boolean}|null=null;
function project3D(x:number,y:number,z=0):Point{return{x:570+(x-y)*.55,y:125+(x+y)*.33-z};}
function projectedPath(ids:string[],z=2){return ids.map(id=>{const p=project3D(node(id).x,node(id).y,z);return `${p.x},${p.y}`}).join(' ');}
function pickNodeFromMap(id:string){selectMapNode(id);}
function selectMapNode(id:string){mapGroupIDs=[];selectedAuxiliary='';if(Date.now()<suppressMapClickUntil)return;if(drawing){pickNode(id);return;}selectedMapNode=id;$('map-node-detail').dataset.groupKey='';const item=node(id),kindLabels:Record<string,string>={loading:'Место погрузки',crusher:'Дробилка и приёмный бункер',junction:'Перекрёсток',garage:'Гараж / стоянка',service:'Ремонт / заправка'};$('map-node-title').textContent=`${id} · ${item.name}`;let detail=kindLabels[item.kind]??item.kind;const entityId=/^E[1-4]$/.test(id)?`excavator-${id.slice(1).padStart(2,'0')}`:/^C[1-3]$/.test(id)?`crusher-${id.slice(1).padStart(2,'0')}`:'';const equipment=entityId?frame?.entities[entityId]:undefined;if(equipment){detail+=` · ${labels[str(equipment,'state')]??str(equipment,'state')}`;if(item.kind==='loading')detail+=` · Очередь: ${n(equipment,'queue_length')}`;if(item.kind==='crusher')detail+=` · Бункер ${fmt(n(equipment,'buffer_t'))} т · Очередь: ${n(equipment,'queue_length')}`;}if(item.kind==='junction'&&topology)detail+=` · Дорог: ${topology.edges.filter(e=>e.from===id||e.to===id).length}`;$('map-node-detail').textContent=detail;$('map-node-info').hidden=false;document.querySelectorAll<SVGGElement>('[data-node]').forEach(g=>g.classList.toggle('selected',g.getAttribute('data-node')===selectedMapNode));}
function draw3DTopology(){
 if(!topology)return;
 const plate=[project3D(55,70),project3D(1085,70),project3D(1085,830),project3D(55,830)];
 const pts=(values:Point[])=>values.map(p=>`${p.x},${p.y}`).join(' ');
 const terrain=$('terrain3d');terrain.replaceChildren();
 terrain.append(el('polygon',{points:pts([plate[1],plate[2],{x:plate[2].x,y:plate[2].y+30},{x:plate[1].x,y:plate[1].y+30}]),class:'terrain-side'}));
 terrain.append(el('polygon',{points:pts([plate[2],plate[3],{x:plate[3].x,y:plate[3].y+30},{x:plate[2].x,y:plate[2].y+30}]),class:'terrain-side terrain-side-front'}));
 terrain.append(el('polygon',{points:pts(plate),class:'terrain-top'}));
 const benches=$('benches3d');benches.replaceChildren();
 const terraceBands=[[[205,165],[825,165],[970,235],[900,305],[310,305],[190,250]],[[205,350],[825,350],[970,420],[900,490],[310,490],[190,435]],[[205,535],[825,535],[970,605],[900,675],[310,675],[190,620]]];
 for(let i=0;i<terraceBands.length;i++){const points=terraceBands[i].map(([x,y])=>project3D(x,y,5+i*2));benches.append(el('polygon',{points:pts(points),class:`bench-contour bench-contour-${i+1}`}));}
 const roads=$('roads3d');roads.replaceChildren();
 for(const edge of topology.edges){const a=node(edge.from),b=node(edge.to),pa=project3D(a.x,a.y,3),pb=project3D(b.x,b.y,3),line=el('line',{x1:pa.x,y1:pa.y,x2:pb.x,y2:pb.y,class:'road3d-bed'}),road=el('line',{x1:pa.x,y1:pa.y-2,x2:pb.x,y2:pb.y-2,class:'road3d-surface'}),mark=el('line',{x1:pa.x,y1:pa.y-2,x2:pb.x,y2:pb.y-2,class:'road3d-center'});roads.append(line,road,mark);const label=el('text',{x:(pa.x+pb.x)/2,y:(pa.y+pb.y)/2-11,class:'distance3d'});label.textContent=`${String(edge.length_km).replace('.',',')} км`;roads.append(label);}
const parking=$('parking3d');parking.replaceChildren();const garage=project3D(node('P').x,node('P').y),parkingTop=(offset:number)=>({x:garage.x+offset,y:garage.y+8+offset*.6}),parkingStart=parkingTop(26),parkingEnd=parkingTop(210);parking.append(el('polygon',{points:pts([parkingStart,parkingEnd,{x:parkingEnd.x,y:parkingEnd.y+140},{x:parkingStart.x,y:parkingStart.y+140}]),class:'parking-pad'}));for(let slot=0;slot<=5;slot++){const top=parkingTop(26+slot*36);parking.append(el('line',{x1:top.x,y1:top.y,x2:top.x,y2:top.y+140,class:'parking-stall'}));}
 const nodes=$('nodes3d');nodes.replaceChildren();
 for(const item of topology.nodes){const p=project3D(item.x,item.y),h=item.kind==='crusher'?50:item.kind==='loading'?38:item.kind==='garage'?34:item.kind==='service'?30:10,w=item.kind==='junction'?13:31,g=el('g',{class:`node3d ${item.kind}`,'data-node':item.id,tabindex:0,role:'button','aria-label':`${item.id} ${item.name}`,'transform':`translate(${p.x} ${p.y})`});
  if(item.kind==='junction'){g.append(el('ellipse',{cx:0,cy:-h,rx:w,ry:9,class:'node3d-top'}),el('path',{d:`M ${-w} ${-h} L ${-w} 0 M ${w} ${-h} L ${w} 0`,class:'node3d-post'}));}
  else{g.append(el('polygon',{points:`${-w},${-h+12} 0,${-h} ${w},${-h+12} 0,${-h+24}`,class:'node3d-face-left'}),el('polygon',{points:`${w},${-h+12} ${w},12 0,24 0,${-h+24}`,class:'node3d-face-right'}),el('polygon',{points:`${-w},${-h+12} 0,${-h} ${w},${-h+12} 0,${-h+24}`,class:'node3d-top'}));const id=el('text',{x:0,y:-h+13,class:'node3d-id'});id.textContent=item.id;g.append(id);const name=el('text',{x:0,y:-h-12,class:'node3d-name'});name.textContent=item.name;g.append(name);}
  g.addEventListener('click',()=>pickNodeFromMap(item.id));g.addEventListener('keydown',e=>{if((e as KeyboardEvent).key==='Enter')pickNodeFromMap(item.id)});nodes.append(g);
 }
 render3DHighlight();
}
function render3DHighlight(){if(!$('route-highlight-3d')||!topology)return;const group=$('route-highlight-3d');group.replaceChildren();let ids=draft;if(ids.length===0&&frame){const p=parse<Plan|null>(get(frame.entities[selected],'route_json','null'),null);ids=p?.nodes??[];}if(ids.length<2)return;const points=ids.map(id=>project3D(node(id).x,node(id).y,8));const path=points.map(p=>`${p.x},${p.y}`).join(' ');group.append(el('polyline',{points:path,class:'selected-route-3d-halo'}),el('polyline',{points:path,class:'selected-route-3d'}));group.append(el('circle',{cx:points[0].x,cy:points[0].y,r:12,class:'route-endpoint route-start'}));const end=points.at(-1)!,before=points.at(-2)!,angle=Math.atan2(end.y-before.y,end.x-before.x),length=22,width=13,base={x:end.x-length*Math.cos(angle),y:end.y-length*Math.sin(angle)},perp={x:Math.sin(angle)*width,y:-Math.cos(angle)*width};group.append(el('polygon',{points:`${end.x},${end.y} ${base.x+perp.x},${base.y+perp.y} ${base.x-perp.x},${base.y-perp.y}`,class:'route-arrow'}),el('circle',{cx:end.x,cy:end.y,r:7,class:'route-endpoint route-finish'}));}
function drawTopology(){if(!topology)return;$('roads').replaceChildren();$('nodes').replaceChildren();for(const e of topology.edges){const a=node(e.from),b=node(e.to);const g=el('g',{});g.append(el('line',{x1:a.x,y1:a.y,x2:b.x,y2:b.y,class:'road-outline'}),el('line',{x1:a.x,y1:a.y,x2:b.x,y2:b.y,class:'road'}));textSVG(g,(a.x+b.x)/2,(a.y+b.y)/2-12,String(e.length_km).replace('.',','),'distance');$('roads').append(g);}
 for(const n of topology.nodes){const g=el('g',{class:`site-node ${n.kind}`,'data-node':n.id,tabindex:0,role:'button','aria-label':`${n.id} ${n.name}`});if(n.kind==='junction'){g.append(el('circle',{cx:n.x,cy:n.y,r:13}));textSVG(g,n.x,n.y-22,n.id,'junction-label');}else{g.append(el('rect',{x:n.x-55,y:n.y-28,width:110,height:56,rx:10}));textSVG(g,n.x,n.y+5,n.id,'node-id');textSVG(g,n.x,n.y+48,n.name,'node-name');if(n.kind==='garage')textSVG(g,n.x,n.y+69,'СТАРТ СМЕНЫ','node-subtitle');if(n.kind==='service')textSVG(g,n.x,n.y+69,'РЕМОНТ · 1 БРИГАДА','node-subtitle');}g.addEventListener('click',()=>selectMapNode(n.id));g.addEventListener('keydown',e=>{if((e as KeyboardEvent).key==='Enter')selectMapNode(n.id)});$('nodes').append(g);}
 const select=$<HTMLSelectElement>('route-select');for(const r of topology.routes){const o=document.createElement('option');o.value=r.id;o.textContent=`${r.id} · ${r.nodes[0]} → ${r.nodes.at(-1)}`;select.append(o);}renderHighlight();}
function pickNode(id:string){if(!drawing)return;if(!topology)return;if(draft.length===0){if(node(id).kind!=='loading'){notice('Начните с погрузки E1–E4');return}}else{if(draft.includes(id)){notice('Этот узел уже есть в пути');return};if(!topology.edges.some(e=>(e.from===draft.at(-1)&&e.to===id)||(e.to===draft.at(-1)&&e.from===id))){notice('Выберите соседний узел по дороге');return};if(node(draft.at(-1)!).kind==='crusher'){notice('Путь уже заканчивается на дробилке');return}}draft.push(id);draftChanged();}
function renderHighlight(){render3DHighlight();const g=$('route-highlight');g.replaceChildren();let nodes=draft;if(nodes.length===0&&frame){const p=parse<Plan|null>(get(frame.entities[selected],'route_json','null'),null);nodes=p?.nodes??[];}if(nodes.length<2)return;const points=nodes.map(id=>{const n=node(id);return `${n.x},${n.y}`}).join(' ');g.append(el('polyline',{points,class:'selected-route'}));}
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
 renderFleet();renderEquipment();renderMaintenance();renderRepairSites();renderServiceVehicles();if(!$('map-node-info').hidden){if(mapGroupIDs.length)showVehicleGroup(mapGroupIDs);else if(selectedAuxiliary)showAuxiliary(selectedAuxiliary);}if(activeTab==='events')renderEvents();if(preview&&previewTarget===selected&&preview.route_revision!==n(f,'route_revision')){preview=null;($('apply') as HTMLButtonElement).disabled=true;$('route-preview').textContent='Назначение изменилось. Выберите путь повторно.';}renderHighlight();}
function renderEquipment(){if(!frame)return;const grid=$('equipment-grid');grid.replaceChildren();for(const id of ['excavator-01','excavator-02','excavator-03','excavator-04','crusher-01','crusher-02','crusher-03']){const f=frame.entities[id],crusher=id.startsWith('crusher');const card=document.createElement('article');card.className=`equipment-card ${crusher?'crusher-card':''}`;const h=document.createElement('h3');h.textContent=`${crusher?'Дробилка C':'Экскаватор E'}${Number(id.slice(-2))}`;const state=document.createElement('span');state.className='equipment-state';state.textContent=str(f,'state')==='idle'?'Свободен':labels[str(f,'state')]??str(f,'state');const line=document.createElement('p');line.textContent=crusher?`${fmt(n(f,'buffer_t'))} / 720 т в бункере`:`${fmt(n(f,'loaded_t'))} т погружено`;const bar=document.createElement('div');bar.className='meter';const fill=document.createElement('i');fill.style.width=crusher?`${n(f,'buffer_t')/720*100}%`:(str(f,'state')==='loading'?'100%':'0');bar.append(fill);const detail=document.createElement('small');detail.textContent=`Очередь: ${n(f,'queue_length')}${crusher?` · мощность ${fmt(n(f,'capacity_tph'))} т/ч`:''}`;card.append(h,state,line,bar,detail);grid.append(card);}}
function appendLog(parent:Element,time:number,title:string,description?:string){const row=document.createElement('div');row.className='event-row';const t=document.createElement('time');t.textContent=clock(time);const text=document.createElement('span');text.textContent=title;if(description){const small=document.createElement('small');small.textContent=description;text.append(small)}row.append(t,text);parent.append(row);}
function renderEvents(){$('event-list').replaceChildren();$('command-list').replaceChildren();if(frame){const events=parse<{sim_time_s:number;text:string}[]>(get(frame.entities['mine-01'],'events_json'),[]);for(const e of [...events].reverse())appendLog($('event-list'),e.sim_time_s,e.text);}for(const r of commandLog)appendLog($('command-list'),r.sim_time_s??0,labels[r.status]??r.status,r.message);}
async function reports(){const runs=await api<Record<string,unknown>[]>('/api/runs');$('report-list').replaceChildren();for(const run of runs){const row=document.createElement('article');row.className='report-row';const title=document.createElement('strong');title.textContent=`Смена ${String(run.run_id).slice(0,8)} · ${clock(Number(run.sim_time_s))}`;const detail=document.createElement('span');detail.textContent=`Переработано ${fmt(Number(run.processed_t))} т · доставлено ${fmt(Number(run.delivered_t))} т`;const b=document.createElement('button');b.textContent='Скачать JSON';b.onclick=()=>{const url=URL.createObjectURL(new Blob([JSON.stringify(run,null,2)],{type:'application/json'}));const a=document.createElement('a');a.href=url;a.download=`shift-${run.run_id}.json`;a.click();setTimeout(()=>URL.revokeObjectURL(url),1000)};row.append(title,detail,b);$('report-list').append(row);}if(runs.length===0)$('report-list').textContent='Завершённых смен пока нет.';}

const ROAD_Z=5;
function vehicleTracks():VehicleTrack[]{
 if(!frame)return [];
 const age=performance.now()-frameAt,running=str(frame.entities['control-01'],'state')==='running';
 return Object.keys(frame.entities).filter(isVehicle).sort().map(id=>{
  const f=frame!.entities[id],old=previous?.run_id===frame!.run_id?previous.entities[id]:undefined;
  let progress=n(f,'edge_progress');
  if(!reducedMotion&&running&&age<interpolationMs&&old&&n(f,'speed_kmh')>0&&str(old,'state')===str(f,'state')&&str(f,'edge_id')&&str(old,'edge_id')===str(f,'edge_id')){
   progress=n(old,'edge_progress')+(progress-n(old,'edge_progress'))*Math.min(1,age/interpolationMs);
  }
  return{id,edgeId:str(f,'edge_id'),nodeId:str(f,'node_id')||'P',progress};
 });
}
function isVehicle(id:string){return /^(truck|service|tow)-/.test(id);}
const interpolationMs=1800;
const reducedMotion=matchMedia('(prefers-reduced-motion: reduce)').matches;
let visualDirty=true,lastVisualKey='';
let currentPlacements=new Map<string,VehiclePlacement>();
let mapGroupIDs:string[]=[];
let selectedAuxiliary='';
function vehicleLabel(id:string){return id.startsWith('truck-')?'HT-'+id.slice(-2):vehicleName(id);}
function vehicleTone(f:Features){
 if(str(f,'failure_severity'))return 'fault';
 if(Boolean(get(f,'maintenance',false))||['diagnosing','repairing','field_diagnosis','field_work'].includes(str(f,'state')))return 'service';
 if(n(f,'payload_t')>0)return 'loaded';
 return n(f,'speed_kmh')>0?'empty':'waiting';
}
function vehicleMark(f:Features){
 const severity=str(f,'failure_severity');
 if(severity)return ({light:'Л',medium:'С',heavy:'Т'} as Record<string,string>)[severity]||'!';
 if(Boolean(get(f,'maintenance',false)))return 'ТО';
 return str(f,'state')==='idle'?'':n(f,'payload_t')>0?'Г':'П';
}
function showVehicleGroup(ids:string[]){
 mapGroupIDs=ids;selectedAuxiliary='';selectedMapNode='';$('map-node-title').textContent=`Техника в группе: ${ids.length}`;
 const detail=$('map-node-detail');const groupKey=ids.join('|');if(detail.dataset.groupKey!==groupKey){detail.replaceChildren();detail.dataset.groupKey=groupKey;}
 for(const id of ids){
  let button=Array.from(detail.querySelectorAll<HTMLButtonElement>('button')).find(item=>item.dataset.vehicle===id);
  if(!button){button=document.createElement('button');button.className='map-group-item';button.type='button';button.dataset.vehicle=id;detail.append(button);}
  button.textContent=`${vehicleLabel(id)} · ${labels[str(frame?.entities[id],'state')]??str(frame?.entities[id],'state')}`;
  button.onclick=()=>{if(id.startsWith('truck-'))selectTruck(id);else showAuxiliary(id);};
 }
 $('map-node-info').hidden=false;
}
function showAuxiliary(id:string){
 const f=frame?.entities[id];mapGroupIDs=[];selectedAuxiliary=id;$('map-node-detail').dataset.groupKey='';$('map-node-title').textContent=vehicleName(id);
 const target=str(f,'target_truck_id');$('map-node-detail').textContent=`${str(f,'state')==='idle'?'Свободна':labels[str(f,'state')]??str(f,'state')} · ${str(f,'edge_id').replace('>',' → ')||str(f,'node_id')}${target?' · '+vehicleLabel(target):''}`;
 $('map-node-info').hidden=false;
}
function syncOverlayViewport(){
 const svg=$<SVGSVGElement>(mapView==='3d'?'map3d':'map'),overlay=$<SVGSVGElement>('map-overlay');
 const rect=svg.getBoundingClientRect(),wrap=document.querySelector('.map-wrap')!.getBoundingClientRect();
 overlay.style.left=`${rect.left-wrap.left}px`;overlay.style.top=`${rect.top-wrap.top}px`;
 overlay.style.width=`${rect.width}px`;overlay.style.height=`${rect.height}px`;
 overlay.setAttribute('viewBox',`${mapBox.x} ${mapBox.y} ${mapBox.width} ${mapBox.height}`);
 const matrix=svg.getScreenCTM();return Math.max(.001,matrix?Math.hypot(matrix.a,matrix.b):rect.width/mapBox.width);
}
const overlayGroups=new Map<string,SVGGElement>();
function renderVehicleMarkers(placements:Map<string,VehiclePlacement>,scale:number){
 const overlay=$('map-overlay');const active=new Set<string>();
 for(const ids of groupVehicles(placements,54/scale)){
  // Show the selected truck at its own anchor, even inside a close group.
  const representative=ids.includes(selected)?selected:ids[0],point=placements.get(representative)!.point;
  const key=ids.join('|');active.add(key);let g=overlayGroups.get(key);
  if(!g){
   g=el('g',{'data-vehicles':key,role:'button',tabindex:0,class:'vehicle-marker'}) as SVGGElement;
   g.append(el('circle',{r:3,class:'physical-anchor'}),el('line',{x1:0,y1:0,x2:0,y2:-27,class:'marker-leader'}),el('rect',{x:-25,y:-44,width:50,height:19,rx:5,class:'marker-background'}),el('text',{x:0,y:-31,class:'marker-label'}),el('title',{}));
   const choose=()=>{if(ids.length>1)showVehicleGroup(ids);else if(representative.startsWith('truck-'))selectTruck(representative);else showAuxiliary(representative);};
   g.addEventListener('click',e=>{e.stopPropagation();choose()});g.addEventListener('keydown',e=>{if((e as KeyboardEvent).key==='Enter'||(e as KeyboardEvent).key===' '){e.preventDefault();choose()}});
   overlay.append(g);overlayGroups.set(key,g);
  }
  const f=frame!.entities[representative],anyFault=ids.some(id=>str(frame!.entities[id],'failure_severity'));
  const label=representative.startsWith('truck-')?representative.slice(-2):representative.startsWith('tow-')?'Э':'С'+Number(representative.slice(-2));
  const mark=representative.startsWith('truck-')?vehicleMark(f):'',text=ids.length>1?`${label} +${ids.length-1}`:`${label}${mark?' · '+mark:''}`;
  g.setAttribute('transform',`translate(${point.x} ${point.y}) scale(${1/scale})`);
  g.setAttribute('class',`vehicle-marker ${anyFault?'fault':vehicleTone(f)} ${ids.includes(selected)?'chosen':''}`);
  g.querySelector('.marker-label')!.textContent=text;
  const title=ids.map(id=>`${vehicleLabel(id)} · ${labels[str(frame!.entities[id],'state')]??str(frame!.entities[id],'state')}`).join('\n');
  g.querySelector('title')!.textContent=title;g.setAttribute('aria-label',title+(ids.length>1?' · открыть список':''));
 }
 for(const [key,g]of overlayGroups)if(!active.has(key)){g.remove();overlayGroups.delete(key)}
}
let lastAnimation = 0;
const truckGroups = new Map<string, SVGGElement>();
const truck3DGroups = new Map<string, SVGGElement>();
function makeTruck3D(id:string,index:number){const number=String(index).padStart(2,'0'),group=el('g',{'data-truck':id,role:'button',tabindex:0,'aria-label':`Самосвал HT-${number}`}) as SVGGElement;const title=el('title',{});title.textContent=`Самосвал HT-${number}`;
 // Side profile: the near side is the only visible side, so it has exactly three wheels.
 group.append(title,el('ellipse',{cx:0,cy:5,rx:30,ry:9,class:'truck3d-shadow'}),el('ellipse',{cx:0,cy:1,rx:35,ry:16,class:'truck3d-ring'}),el('polygon',{points:'-26,-3 -21,-6 19,-5 25,-1 23,3 -23,3',class:'truck3d-chassis'}));
 // Dump body: a tall rear box with a clear rim and a solid, simple side panel.
 group.append(el('polygon',{points:'-26,-15 -22,-21 3,-21 10,-15 -5,-9 -24,-10',class:'bed-top'}),el('polygon',{points:'-26,-15 -5,-9 10,-15 8,-2 -23,-3',class:'bed-side bed-near'}),el('polygon',{points:'-26,-15 -24,-10 -23,-3 -26,-5',class:'bed-tail'}),el('line',{x1:-24,y1:-10,x2:-5,y2:-5,class:'bed-rim'}));
 group.append(el('polygon',{points:'-21,-15 -15,-19 2,-19 6,-15 -6,-11 -20,-12',class:'truck3d-cargo'}),el('polygon',{points:'-17,-15 -14,-18 -11,-15 -14,-13',class:'truck3d-rock rock-a'}),el('polygon',{points:'-9,-14 -6,-17 -3,-14 -6,-12',class:'truck3d-rock rock-b'}),el('polygon',{points:'-2,-14 1,-17 4,-14 1,-12',class:'truck3d-rock rock-c'}),el('line',{x1:-5,y1:2,x2:1,y2:-8,class:'truck3d-hydraulic'}));
 // Compact cab facing right. The near-side window and windshield are separate dark panes.
 group.append(el('polygon',{points:'7,-15 11,-22 22,-22 26,-16 26,-2 8,-2',class:'cab-side'}),el('polygon',{points:'11,-22 14,-25 23,-24 22,-22',class:'cab-top'}),el('polygon',{points:'8,-15 11,-22 14,-21 13,-12',class:'cab-front'}),el('polygon',{points:'14,-20 21,-20 21,-14 13,-13',class:'truck3d-window cab-window-side'}),el('polygon',{points:'22,-20 24,-16 24,-11 22,-12',class:'truck3d-window cab-window-front'}),el('line',{x1:24,y1:-2,x2:29,y2:-1,class:'truck3d-bumper'}),el('circle',{cx:25,y:-6,r:1.2,class:'truck3d-light'}));
 // Draw three near-side wheels in front of the chassis, evenly spaced along the truck.
 for(const x of [-18,-4,12]){group.append(el('circle',{cx:x,cy:3.5,r:4.8,class:'truck3d-tire'}),el('circle',{cx:x,cy:3.5,r:2,class:'truck3d-hub'}));}
 const label=el('text',{x:0,y:19,class:'truck3d-number'});label.textContent=`HT-${number}`;label.setAttribute('display','none');group.append(label);group.addEventListener('click',e=>{e.stopPropagation();selectTruck(id)});group.addEventListener('keydown',e=>{if((e as KeyboardEvent).key==='Enter')selectTruck(id)});return group;}
type ModelAsset={scene:THREE.Object3D;longest:number;yaw:number};
type ModelInstance={root:THREE.Group;orientation:THREE.Group;asset:ModelAsset;length:number};
let modelContextLost=false;
let modelRenderer:THREE.WebGLRenderer|undefined;
let modelScene:THREE.Scene|undefined;
let modelCamera:THREE.OrthographicCamera|undefined;
let threeRuntime:typeof import('three')|undefined;
let modelLoadPromise:Promise<void>|undefined;
let modelViewport={left:0,top:0,width:0,height:0,pixelRatio:0};
let modelAssets:Record<string,ModelAsset>={};
const modelInstances=new Map<string,ModelInstance>();
// The truck GLB is diagonal in its XZ ground plane. PCA finds its length axis;
// the higher end is the cab, so it becomes the model's forward direction.
function truckModelOrientation(scene:THREE.Object3D){const three=threeRuntime!,samples:{x:number;y:number;z:number}[]=[],point=new three.Vector3();scene.traverse(object=>{if(!(object instanceof three.Mesh))return;const positions=object.geometry.getAttribute('position');if(!positions)return;for(let i=0;i<positions.count;i++){point.set(positions.getX(i),positions.getY(i),positions.getZ(i)).applyMatrix4(object.matrixWorld);samples.push({x:point.x,y:point.y,z:point.z});}});if(samples.length<2)throw new Error('У модели самосвала нет геометрии для определения направления');const meanX=samples.reduce((sum,p)=>sum+p.x,0)/samples.length,meanZ=samples.reduce((sum,p)=>sum+p.z,0)/samples.length;let xx=0,zz=0,xz=0;for(const p of samples){const x=p.x-meanX,z=p.z-meanZ;xx+=x*x;zz+=z*z;xz+=x*z;}xx/=samples.length;zz/=samples.length;xz/=samples.length;const eigenvalue=(xx+zz+Math.hypot(xx-zz,2*xz))/2;let axisX=xz,axisZ=eigenvalue-xx;if(Math.hypot(axisX,axisZ)<1e-8){axisX=xx>=zz?1:0;axisZ=xx>=zz?0:1;}const axisLength=Math.hypot(axisX,axisZ);axisX/=axisLength;axisZ/=axisLength;const projected=samples.map(p=>({along:(p.x-meanX)*axisX+(p.z-meanZ)*axisZ,height:p.y})).sort((a,b)=>a.along-b.along),endCount=Math.max(1,Math.floor(projected.length*.1)),endHeight=(start:number)=>projected.slice(start,start+endCount).reduce((sum,p)=>sum+p.height,0)/endCount;if(endHeight(0)>endHeight(projected.length-endCount)){axisX=-axisX;axisZ=-axisZ;}return{yaw:Math.atan2(axisZ,axisX),longest:projected[projected.length-1].along-projected[0].along};}
function makeModelInstance(asset:ModelAsset,length:number){
 const root=new threeRuntime!.Group(),tilt=new threeRuntime!.Group(),orient=new threeRuntime!.Group(),model=asset.scene.clone(true);
 model.scale.setScalar(length/asset.longest);model.position.set(0,0,0);orient.rotation.y=asset.yaw;
 // Match the 3D model ground plane to the map's axonometric slope (0.33 / 0.55).
 tilt.rotation.x=Math.PI-Math.asin(.6);orient.add(model);tilt.add(orient);root.add(tilt);modelScene!.add(root);return{root,orientation:orient,asset,length};
}
function setModelPosition(key:string,asset:ModelAsset,length:number,x:number,y:number,heading=0){let instance=modelInstances.get(key);if(!instance){instance=makeModelInstance(asset,length);modelInstances.set(key,instance);}const svg=$<SVGSVGElement>('map3d'),point=svg.createSVGPoint();point.x=x;point.y=y;const screen=point.matrixTransform(svg.getScreenCTM()!),rect=svg.getBoundingClientRect(),ctm=svg.getScreenCTM()!,scale=Math.hypot(ctm.a,ctm.b),screenLimit=key.startsWith('truck-')?48:key.startsWith('excavator-')?88:56,angle=heading*Math.PI/180;
 // Turn around the model's vertical axis, then let the fixed camera tilt show the near wheels.
 // Rotating the whole screen-space group made the model lean whenever its road was diagonal.
 const modelYaw=Math.atan2(Math.sin(angle),.6*Math.cos(angle));instance.root.position.set(screen.x-rect.left,screen.y-rect.top,0);instance.root.scale.setScalar(Math.min(scale,screenLimit/length));instance.orientation.rotation.y=asset.yaw+modelYaw;instance.root.rotation.z=0;}
  // Keep WebGL inside the SVG viewport so panning and zooming clip models at the same map edges.
  function syncGLBModels(){
 if(modelContextLost||!modelRenderer||!modelScene||!modelCamera)return;
 const svg=$<SVGSVGElement>('map3d'),canvas=$<HTMLCanvasElement>('map3d-models'),wrap=document.querySelector<HTMLElement>('.map-wrap')!,svgRect=svg.getBoundingClientRect(),wrapRect=wrap.getBoundingClientRect(),left=svgRect.left-wrapRect.left,top=svgRect.top-wrapRect.top,dpr=Math.min(window.devicePixelRatio||1,2);
 if(svgRect.width<=0||svgRect.height<=0)return;
 if(left!==modelViewport.left||top!==modelViewport.top||svgRect.width!==modelViewport.width||svgRect.height!==modelViewport.height||dpr!==modelViewport.pixelRatio){
  modelViewport={left,top,width:svgRect.width,height:svgRect.height,pixelRatio:dpr};
  canvas.style.inset='auto';canvas.style.left=`${left}px`;canvas.style.top=`${top}px`;canvas.style.right='auto';canvas.style.bottom='auto';canvas.style.width=`${svgRect.width}px`;canvas.style.height=`${svgRect.height}px`;
  modelRenderer.setPixelRatio(dpr);modelRenderer.setSize(svgRect.width,svgRect.height,false);modelCamera.left=0;modelCamera.right=svgRect.width;modelCamera.top=0;modelCamera.bottom=svgRect.height;modelCamera.updateProjectionMatrix();
 }
 const ctm=svg.getScreenCTM(),mapScale=ctm?Math.hypot(ctm.a,ctm.b):1;
 if(ctm&&frame&&topology){
  for(const [key,instance]of modelInstances)if(isVehicle(key))instance.root.visible=false;
  for(const [id,placement]of currentPlacements){
   const asset=assetFor(id);if(!asset)continue;
   setModelPosition(id,asset,id.startsWith('truck-')?26:id.startsWith('tow-')?25:20,placement.point.x,placement.point.y,placement.heading);
   modelInstances.get(id)!.root.visible=true;
  }
  for(const id of ['E1','E2','E3','E4']){
   const asset=modelAssets.excavator;if(!asset)continue;
   const p=project3D(node(id).x,node(id).y,5),edge=topology.edges.find(item=>item.from===id||item.to===id),neighbor=edge?node(edge.from===id?edge.to:edge.from):undefined;
   const next=neighbor?project3D(neighbor.x,neighbor.y,5):undefined,dx=next?p.x-next.x:1,dy=next?p.y-next.y:0,heading=Math.atan2(dy,dx)*180/Math.PI;
   setModelPosition(`excavator-${id}`,asset,24,p.x-20/mapScale,p.y+8/mapScale,heading);
  }

 }
 modelRenderer.render(modelScene,modelCamera);
}
function createTowAsset(three:typeof import('three')):ModelAsset{
 const vehicle=new three.Group();
 const box=(x:number,y:number,z:number,w:number,h:number,d:number,color:number)=>{
  const mesh=new three.Mesh(new three.BoxGeometry(w,h,d),new three.MeshBasicMaterial({color}));mesh.position.set(x,y,z);vehicle.add(mesh);
 };
 box(0,.48,0,3.4,.35,1.35,0x384d75);box(1,.95,0,1.1,.8,1.2,0x4565a5);box(1.2,1.13,0,.45,.3,1.23,0xbadbdc);
 box(-.65,.8,0,1.6,.3,1.05,0xc49939);box(-.9,1.4,0,.23,1.1,.23,0xd8b350);box(-.2,1.9,0,1.7,.17,.17,0xd8b350);
 box(.55,1.55,0,.1,.6,.1,0x394b53);
 for(const x of [-1.1,1])for(const z of [-.72,.72]){
  const wheel=new three.Mesh(new three.CylinderGeometry(.36,.36,.22,12),new three.MeshBasicMaterial({color:0x222c32}));wheel.rotation.x=Math.PI/2;wheel.position.set(x,.36,z);vehicle.add(wheel);
 }
 return{scene:vehicle,longest:3.4,yaw:0};
}
async function initializeGLBModels(){
 const canvas=$<HTMLCanvasElement>('map3d-models');
 $<HTMLButtonElement>('model-retry').disabled=true;
 try{
  const [three,loaderModule]=await Promise.all([import('three'),import('three/addons/loaders/GLTFLoader.js')]);threeRuntime=three;
  if(!modelRenderer){
   modelRenderer=new three.WebGLRenderer({canvas,alpha:true,antialias:true,powerPreference:'low-power'});
   modelRenderer.setClearColor(0x000000,0);modelRenderer.outputColorSpace=three.SRGBColorSpace;
   modelScene=new three.Scene();modelCamera=new three.OrthographicCamera(0,1,0,1,.1,3000);modelCamera.position.z=1000;
   modelScene.add(new three.HemisphereLight(0xffffff,0x647266,2));
   const key=new three.DirectionalLight(0xffffff,2.3);key.position.set(-250,-350,800);modelScene.add(key);
   canvas.addEventListener('webglcontextlost',e=>{e.preventDefault();modelContextLost=true;canvas.hidden=true;visualDirty=true;notice('Графика 3D временно недоступна. Схема и управление продолжают работать.',true)});
   canvas.addEventListener('webglcontextrestored',()=>{modelContextLost=false;canvas.hidden=mapView!=='3d';visualDirty=true});
  }
  modelAssets.tow??=createTowAsset(three);
  const loader=new loaderModule.GLTFLoader();
  const files=[{key:'truck',path:'/models/haul-truck.glb'},{key:'excavator',path:'/models/excavator.glb'},{key:'pickupLight',path:'/models/pickup-light.glb'},{key:'pickupSupport',path:'/models/pickup-support.glb'}];
  const results=await Promise.allSettled(files.filter(file=>!modelAssets[file.key]).map(async file=>{
   const gltf=await loader.loadAsync(file.path),model=gltf.scene;
   model.traverse(object=>{
    if(!(object instanceof three.Mesh))return;
    const replace=(material:THREE.Material)=>{const source=material as THREE.MeshStandardMaterial;return new three.MeshBasicMaterial({color:source.color??0xffffff,map:source.map??null,transparent:source.transparent,opacity:source.opacity,alphaTest:source.alphaTest,side:three.DoubleSide,toneMapped:false})};
    object.material=Array.isArray(object.material)?object.material.map(replace):replace(object.material);
   });
   model.updateMatrixWorld(true);
   const bounds=new three.Box3().setFromObject(model),size=bounds.getSize(new three.Vector3()),center=bounds.getCenter(new three.Vector3());
   const orientation=file.key==='truck'?truckModelOrientation(model):{yaw:size.z>=size.x?Math.PI/2:0,longest:Math.max(size.x,size.z)};
   if(!Number.isFinite(orientation.longest)||orientation.longest<=0)throw new Error('Пустая геометрия модели');
   const normalized=new three.Group();model.position.x-=center.x;model.position.z-=center.z;model.position.y-=bounds.min.y;normalized.add(model);
   modelAssets[file.key]={scene:normalized,longest:orientation.longest,yaw:orientation.yaw};
  }));
  const failed=results.filter(r=>r.status==='rejected');
  $('model-retry').hidden=failed.length===0;
  if(failed.length){console.warn('Некоторые модели 3D не загрузились',failed);notice('Часть моделей недоступна. Показана схема; загрузку можно повторить.',true)}
  canvas.hidden=mapView!=='3d'||modelContextLost;visualDirty=true;
 }catch(error){
  console.error('Не удалось загрузить модели 3D',error);canvas.hidden=true;$('model-retry').hidden=false;
  notice('Модели 3D недоступны. Схематичная карта и управление сохранены.',true);visualDirty=true;
 }finally{$<HTMLButtonElement>('model-retry').disabled=false;}
}
function animate(now:number){
 requestAnimationFrame(animate);
 if(now-lastAnimation<1000/30||!frame||!topology)return;lastAnimation=now;
 const age=performance.now()-frameAt,live=age<10000;
 const connected=$('connection');connected.textContent=live?'● Участок на связи':`● Данные задерживаются · ${Math.floor(age/1000)} с`;connected.classList.toggle('offline',!live);
 const running=str(frame.entities['control-01'],'state')==='running';
 const key=`${frame.run_id}:${frame.snapshot_seq}:${selected}:${mapView}`;
 if(!visualDirty&&lastVisualKey===key&&(!running||age>=interpolationMs||reducedMotion))return;
 lastVisualKey=key;visualDirty=false;
 const tracks=vehicleTracks(),flat=placeVehicles(tracks,topology.nodes,(x,y)=>({x,y}));
 currentPlacements=placeVehicles(tracks,topology.nodes,(x,y)=>project3D(x,y,ROAD_Z));
 const scale=syncOverlayViewport();
 if(mapView==='2d'){
  for(const track of tracks){
   const {id}=track,p=flat.get(id);if(!p)continue;const f=frame.entities[id];let group=truckGroups.get(id);
   if(!group){
    group=el('g',{'data-truck':id,class:'truck'}) as SVGGElement;
    group.append(el('rect',{x:-12,y:-7,width:24,height:14,rx:4,class:'truck-body'}));$('trucks').append(group);truckGroups.set(id,group);
   }
   group.setAttribute('transform',`translate(${p.point.x} ${p.point.y}) rotate(${p.heading})`);
   group.querySelector('.truck-body')!.setAttribute('fill',str(f,'failure_severity')?'#bb3e37':Boolean(get(f,'maintenance',false))?'#8563a5':id.startsWith('tow-')?'#4154a2':id.startsWith('service-')?'#26834e':n(f,'payload_t')>0?'#d99229':n(f,'speed_kmh')>0?'#378b9e':'#75878a');
  }
 }else{
  for(const track of tracks){
   const {id}=track,p=currentPlacements.get(id);if(!p)continue;const f=frame.entities[id];let g=truck3DGroups.get(id);
   if(!g){g=id.startsWith('truck-')?makeTruck3D(id,Number(id.slice(-2))):makeAuxiliary3D(id);$('trucks3d').append(g);truck3DGroups.set(id,g)}
   g.setAttribute('transform',`translate(${p.point.x} ${p.point.y}) rotate(${p.heading})`);
   g.setAttribute('class',`truck3d ${id.startsWith('truck-')?'haul':'auxiliary'} ${vehicleTone(f)} ${id===selected?'selected':''} ${assetFor(id)?'glb-model-active':''}`);
  }
  syncGLBModels();
 }
 renderVehicleMarkers(mapView==='3d'?currentPlacements:flat,scale);
}
function makeAuxiliary3D(id:string){
 const g=el('g',{'data-truck':id}) as SVGGElement;
 g.append(el('rect',{x:-14,y:-6,width:28,height:12,rx:3,fill:id.startsWith('tow-')?'#4154a2':'#26834e'}),el('rect',{x:6,y:-11,width:9,height:8,rx:2,fill:'#d1e8e5'}));
 if(id.startsWith('tow-'))g.append(el('path',{d:'M -10 -5 L -5 -19 L 9 -19 L 9 -10',fill:'none',stroke:'#d0a13f','stroke-width':3}));
 return g;
}
function assetFor(id:string){if(modelContextLost)return undefined;return id.startsWith('truck-')?modelAssets.truck:id==='service-01'?modelAssets.pickupLight:id==='service-02'?modelAssets.pickupSupport:modelAssets.tow;}

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
 const mapTime=$<HTMLButtonElement>('map-time');mapTime.textContent=mode==='running'?'Пауза смены':mode==='ready'?'Начать смену':'Продолжить смену';mapTime.disabled=role!=='dispatcher'||ended;
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
function setMapView(view:'2d'|'3d'){visualDirty=true;mapView=view;try{localStorage.setItem('team050-map-view',view)}catch{};$('map3d').removeAttribute('hidden');$('map').style.display=view==='2d'?'block':'none';$('map3d').style.display=view==='3d'?'block':'none';$('map3d-models').hidden=view!=='3d'||modelContextLost;$('map').setAttribute('aria-hidden',String(view!=='2d'));$('map3d').setAttribute('aria-hidden',String(view!=='3d'));$('view-2d').classList.toggle('active',view==='2d');$('view-3d').classList.toggle('active',view==='3d');$('view-2d').setAttribute('aria-pressed',String(view==='2d'));$('view-3d').setAttribute('aria-pressed',String(view==='3d'));$('map-scale').textContent=view==='3d'?'Аксонометрия · высота условная':'План · расстояния в км';$('map-legend-note').textContent=view==='3d'?'Точка — позиция · +число — группа':'Точка — позиция · +число — группа';$('map-hint').textContent=view==='3d'?'Уступы условные · высоты и масштаб не заданы':'Колесо мыши — масштаб · перетаскивайте карту';if(view==='3d'){if(modelRenderer)syncGLBModels();else if(!modelLoadPromise)modelLoadPromise=initializeGLBModels();}renderHighlight();}
function applyMapBox(){visualDirty=true;for(const id of ['map','map3d'])$(id).setAttribute('viewBox',`${mapBox.x} ${mapBox.y} ${mapBox.width} ${mapBox.height}`);$('map-zoom-reset').textContent=`${Math.round(mapBounds.width/mapBox.width*100)}%`;}
function setMapBox(next:typeof mapBox){const width=Math.max(250,Math.min(mapBounds.width,next.width)),height=width*mapBounds.height/mapBounds.width,x=Math.max(0,Math.min(mapBounds.width-width,next.x)),y=Math.max(0,Math.min(mapBounds.height-height,next.y));mapBox={x,y,width,height};applyMapBox();}
function zoomMap(factor:number,anchor?:{x:number;y:number}){const center=anchor??{x:mapBox.x+mapBox.width/2,y:mapBox.y+mapBox.height/2},width=Math.max(250,Math.min(mapBounds.width,mapBox.width*factor)),ratio=width/mapBox.width;setMapBox({x:center.x-(center.x-mapBox.x)*ratio,y:center.y-(center.y-mapBox.y)*ratio,width,height:width*mapBounds.height/mapBounds.width});}
let suppressMapClickUntil=0;
function bindMapNavigation(svg:SVGSVGElement){svg.addEventListener('wheel',e=>{e.preventDefault();const p=svg.createSVGPoint();p.x=e.clientX;p.y=e.clientY;const a=p.matrixTransform(svg.getScreenCTM()!.inverse());zoomMap(e.deltaY<0?.86:1.16,{x:a.x,y:a.y});},{passive:false});svg.addEventListener('pointerdown',e=>{if((e.target as Element).closest('[role="button"]'))return;mapDrag={pointerId:e.pointerId,x:e.clientX,y:e.clientY,box:{...mapBox},svg,moved:false};svg.setPointerCapture(e.pointerId);svg.classList.add('dragging');});svg.addEventListener('pointermove',e=>{if(!mapDrag||mapDrag.pointerId!==e.pointerId)return;const rect=svg.getBoundingClientRect(),dx=e.clientX-mapDrag.x,dy=e.clientY-mapDrag.y;if(Math.abs(dx)+Math.abs(dy)>4)mapDrag.moved=true;if(mapDrag.moved)setMapBox({x:mapDrag.box.x-dx*mapDrag.box.width/rect.width,y:mapDrag.box.y-dy*mapDrag.box.height/rect.height,width:mapDrag.box.width,height:mapDrag.box.height});});const end=(e:PointerEvent)=>{if(!mapDrag||mapDrag.pointerId!==e.pointerId)return;if(mapDrag.moved)suppressMapClickUntil=Date.now()+250;mapDrag.svg.classList.remove('dragging');mapDrag=null;};svg.addEventListener('pointerup',end);svg.addEventListener('pointercancel',end);}
for(const id of ['map','map3d'])bindMapNavigation($(id));
$('map-time').onclick=()=>act(()=>send(str(frame?.entities['control-01'],'state')==='running'?'pause_shift':str(frame?.entities['control-01'],'state')==='ready'?'start_shift':'resume_shift'));
$('map-zoom-in').onclick=()=>zoomMap(.8);$('map-zoom-out').onclick=()=>zoomMap(1.25);$('map-zoom-reset').onclick=()=>setMapBox({...mapBounds});
$('map-expand').onclick=()=>{visualDirty=true;const expanded=$('map-panel').classList.toggle('map-expanded');$('map-time').hidden=!expanded;document.body.classList.toggle('map-expanded',expanded);$('map-expand').textContent=expanded?'Свернуть':'Развернуть';$('map-expand').setAttribute('aria-pressed',String(expanded));};
document.addEventListener('keydown',e=>{if(e.key==='Escape'&&$('map-panel').classList.contains('map-expanded'))$('map-expand').click();});
$('map-node-close').onclick=()=>{mapGroupIDs=[];selectedAuxiliary='';$('map-node-info').hidden=true;selectedMapNode='';document.querySelectorAll<SVGGElement>('[data-node]').forEach(g=>g.classList.remove('selected'));};$('view-2d').onclick=()=>setMapView('2d');
$('view-3d').onclick=()=>setMapView('3d');
$('model-retry').onclick=()=>{modelLoadPromise=initializeGLBModels()};

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
async function refreshPendingCommands(){
 if(pollingCommands)return;
 const ids=[...pendingCommands].filter(([,at])=>Date.now()-at>8000).slice(0,3).map(([id])=>id);
 if(!ids.length)return;pollingCommands=true;
 try{for(const id of ids){pendingCommands.set(id,Date.now());try{const result=await api<{result:CommandResult}>(`/api/commands/${encodeURIComponent(id)}`);logCommand(result.result)}catch{/* Durable outbox and SSE reconnect continue independently. */}}}finally{pollingCommands=false}
}
setInterval(()=>{void refreshPendingCommands()},3000);
function receive(next:Frame){if(frame&&frame.run_id===next.run_id&&frame.snapshot_seq>=next.snapshot_seq)return;if(lastRun!==next.run_id){previous=null;draft=[];preview=null;pendingCommands.clear();lastRun=next.run_id;($('apply') as HTMLButtonElement).disabled=true;}else previous=frame;frame=next;frameAt=performance.now();if(topology)render();}
async function boot(){token=(await api<{token:string}>('/api/session')).token;const es=new EventSource('/api/stream');es.onmessage=e=>{try{const v=JSON.parse(e.data);if(v.type==='state')receive(v.data);if(v.type==='command')logCommand(v.data.result);}catch(err){console.error(err)}};es.onerror=()=>{$('connection').textContent='● Восстановление связи';};const load=async()=>{try{if(!topology){topology=await api<Topology>('/api/topology');drawTopology();draw3DTopology();}const v=await api<{frame:Frame|null;stale:boolean}>('/api/state');if(v.frame)receive(v.frame);if(frame&&topology)render();}catch{ /* the platform may still be starting */ }};await load();setInterval(()=>{if(!topology||!frame||performance.now()-frameAt>10000)void load();},3000);requestAnimationFrame(animate);}
new ResizeObserver(()=>{visualDirty=true}).observe(document.querySelector('.map-wrap')!);
window.addEventListener('resize',()=>{visualDirty=true});
try{if(localStorage.getItem('team050-map-view')==='3d')setMapView('3d')}catch{}
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
