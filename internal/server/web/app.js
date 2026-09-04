const $=selector=>document.querySelector(selector);
const gamesEl=$('#games'),seriesEl=$('#series-grid'),emptyEl=$('#empty'),noResults=$('#no-results'),notice=$('#notice');
const locale=$('#locale'),search=$('#search'),gameDetailDialog=$('#game-detail-dialog'),webPlayerDialog=$('#web-player-dialog'),webNetplayDialog=$('#web-netplay-dialog'),seriesDialog=$('#series-dialog'),gameDialog=$('#game-dialog'),mergeDialog=$('#merge-dialog'),editionDialog=$('#edition-dialog');
const seriesForm=$('#series-form'),gameForm=$('#game-form'),editionForm=$('#edition-form');
const seriesCount=$('#series-count'),gameCount=$('#game-count'),editionCount=$('#edition-count'),platformCount=$('#platform-count'),resultCount=$('#result-count');
let allSeries=[],allGames=[],devices=[],saveRevisions=[],saveStreams=[],saveBindings=[],saveCompatibilityGroups=[],syncSessions=[],inventoryMatchReviews=[],editingSeriesID='',editingGameID='',editingEditionID='',editingRuntimeKind='',editingRuntimeID='',editingCustomPlatformID='',libraryMode='list',packageProfiles=[],configTemplatePresets=[],packageTemplateDrafts=[],lastPackagePlanID='',platformPresets=[],customPlatforms=[],importSources=[],librarySources=[],sourceScans=[],hashSources=[],hashPackFile=null,hashPackPreview=null,sourceAdapters=[],frontendAdapters=[],deviceProfiles=[],emulatorDrivers=[],retroarchCores=[],coreMappings=[],launchBindings=[],runtimeImportHints=[],selectedRuntimeHintID='',runtimeHintBatchPreview=null,lastImportRequest=null,lastImportPreviewToken='',lastSourceScanID='',lastImportCandidates=[],hardwareAcceptanceReport=null,hardwareAcceptancePreview=null,managedCleanupPreview=null,managedCleanupRuns=[],gameMergePreview=null,mediaObjectURLs=[],libraryMediaObjectURLs=[],capabilities={features:{web_emulation:false,web_netplay:false}},webEmulatorReadiness=null,webNetplayReadiness=null;
const inventoryMatchSelections=new Map(),inventoryMatchPreviews=new Map();
const gameDetails=new Map();
let hardwareAcceptanceReviewedDrivers=new Set();
let hardwareAcceptanceCommitStatus=null;
let supportReadiness=null;
let pairingCodeView=null;

const esc=s=>String(s??'').replace(/[&<>'"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[c]));
const tr=s=>globalThis.uiI18n?.t(s)||s;
const typeName={original:'原版',translation:'汉化',hack:'改版',revision:'修订',homebrew:'自制',other:'其他'};
const artifactRoleName={rom:'ROM',disc:'光盘',executable:'可执行文件',patch:'补丁',dlc:'DLC',update:'更新',other:'其他'};
const mediaKindName={cover:'封面',box_front:'盒装正面',box_back:'盒装背面',box_spine:'盒装侧脊',logo:'Logo',screenshot:'截图',title_screen:'标题画面',background:'背景',fanart:'同人画',marquee:'Marquee',bezel:'装饰边框',manual:'手册',video:'视频',music:'音乐',cartridge:'卡带',poster:'海报',banner:'横幅',tile:'磁贴',other:'其他'};
const mediaLocaleName={'':'不限定语言','zh-CN':'简体中文','zh-TW':'繁體中文',ja:'日本語',en:'English'};
const mediaStatusName={unverified:'尚未检查',available:'文件可用',missing:'文件缺失',changed:'内容已变化',unsafe:'路径不安全'};
const relationName={mainline:'正传',port:'移植',remake:'重制',spinoff:'外传',collection:'合集',other:'其他'};
const syncStatusName={proposed:'准备',negotiating:'准备',transferring:'传输中',verifying:'传输中',complete:'已完成',partial:'部分完成',aborted:'已撤销',failed:'失败'};
const deviceStatusName={active:'已连接',offline:'离线',revoked:'已撤销'};
const webPlayablePlatforms=new Set();
const webPlayableExtensions=new Set();
const webPlayableExtensionsByPlatform=new Map();
const webPlayableMinimumBytesByPlatform=new Map();
const apiPath=path=>path==='/api'?'/api/v1':path.startsWith('/api/')?`/api/v1/${path.slice(5)}`:path;
const apiErrorMessages={import_preview_stale:'来源或资料库已在预览后发生变化，请重新生成预览。',hash_pack_preview_stale:'识别包已在预览后发生变化，请重新检查同一个文件。',hash_release_conflict:'相同来源与发布版本已经对应另一份内容；请使用新的发布版本。',import_batch_conflict:'整批导入已取消，没有任何条目入库。请重新预览并处理冲突。',platform_definition_conflict:'整合包中的平台定义与本地设置冲突；未导入任何条目。请保留本地设置或修正清单后重新预览。',platform_definition_disabled:'整合包引用的平台在本地已停用；请先人工启用，再重新生成预览。',platform_key_conflict:'整合包中的平台标识、别名或 ES-DE 目录与本地平台注册表冲突。',runtime_definition_conflict:'整合包中的运行配置与本地同名定义不一致；没有导入任何条目。请保留本地设置或改用新的稳定标识。',runtime_definition_disabled:'整合包引用的运行配置在本地已停用；请先人工启用，再重新生成预览。',runtime_hint_batch_stale:'建议、版本或运行配置已在预览后变化；请重新预览整批操作。',runtime_hint_batch_conflict:'至少一个版本已存在这个设备的启动绑定；整批操作未写入任何内容。',hardware_acceptance_stale:'验收报告或适配关系已在预览后发生变化，请重新生成预览。',managed_cleanup_stale:'受管文件或资料库关联在预览后发生变化，请重新扫描。',managed_storage_unsafe:'受管存储中存在符号链接或特殊文件；为避免越界，未移动任何文件。',cleanup_restore_conflict:'原位置已有文件；为避免覆盖，没有恢复任何文件。',cleanup_recovery_damaged:'恢复区文件缺失或发生变化；没有恢复任何文件。',artifact_outside_library:'ROM 路径必须位于资料库根目录内。',artifact_missing:'没有找到这个 ROM 文件或目录；未创建任何版本或 ROM 记录。',artifact_unreadable:'无法安全读取并计算这个 ROM 的哈希；未创建任何记录。',inventory_match_stale:'ROM 身份或候选版本已经变化，请重新预览后再确认。',inventory_match_not_ambiguous:'这个 ROM 已经不需要人工确认。',save_runtime_not_attested:'设备上的模拟器或核心身份尚未通过精确核验；不会读取、下载或覆盖存档。',web_emulation_unavailable:'这个版本暂时不能在浏览器运行。请检查平台、ROM 与网页模拟器配置。',web_emulation_rom_invalid:'这个文件不是所选平台可识别的 ROM；请关联正确的 ROM 后再运行。',web_emulation_rom_changed:'ROM 内容已经变化；请重新检查文件后再运行。',web_netplay_invite_invalid:'邀请口令无效，请向房主获取完整口令。',invalid_invitation:'邀请口令无效或已经过期。',compatibility_mismatch:'本地 ROM 或网页模拟器版本与房主不一致，无法加入。',session_full:'房间已经满员。',web_netplay_signal_unavailable:'网页联机服务暂时不可用。'};
async function apiResponse(path,options,headers){
  const r=await fetch(path,{...options,headers:{...headers,...(options.headers||{})}});
  if(!r.ok){let message=`HTTP ${r.status}`,code='';try{const body=await r.json();code=body?.error?.code||'';if(code==='game_merge_stale')message=tr('游戏、版本、ROM、媒体或系列已发生变化，请重新确认合并。');else message=apiErrorMessages[code]?tr(apiErrorMessages[code]):body?.error?.message||message}catch{}if(r.status===401&&code!=='invalid_invitation'){showAuth();message=tr('访问令牌无效或已失效')}throw new Error(message)}
  if(r.status===204)return null;return r.json();
}
function pagedAPIPath(path,limit,offset){
  const url=new URL(apiPath(path),location.origin);url.searchParams.set('limit',String(limit));url.searchParams.set('offset',String(offset));return `${url.pathname}${url.search}`;
}
async function api(path,options={}){
  const headers=options.body instanceof FormData?{}:{'Content-Type':'application/json'};
  const token=sessionStorage.getItem('game-library-token');if(token)headers.Authorization=`Bearer ${token}`;
  const method=String(options.method||'GET').toUpperCase(),url=new URL(apiPath(path),location.origin),explicitPage=url.searchParams.has('limit')||url.searchParams.has('offset'),autoPage=method==='GET'&&!explicitPage;
  const pageLimit=200,body=await apiResponse(autoPage?pagedAPIPath(path,pageLimit,0):apiPath(path),options,headers);
  if(!body?.pagination||!Array.isArray(body.data))return body;
  if(!autoPage)return body.data;
  const total=body.pagination.total;
  if(!Number.isInteger(total)||total<0||body.pagination.offset!==0||body.data.length>total)throw new Error(tr('资料库列表在分页加载期间发生变化，请重试。'));
  const items=[...body.data];
  for(let offset=pageLimit;offset<total;offset+=pageLimit){
    const next=await apiResponse(pagedAPIPath(path,pageLimit,offset),options,headers);
    if(!next?.pagination||!Array.isArray(next.data)||next.pagination.total!==total||next.pagination.offset!==offset)throw new Error(tr('资料库列表在分页加载期间发生变化，请重试。'));
    items.push(...next.data);
  }
  if(items.length!==total)throw new Error(tr('资料库列表在分页加载期间发生变化，请重试。'));
  return items;
}
function showAuth(){const dialog=$('#auth-dialog');if(!dialog.open)dialog.showModal()}
let toastTimer;
function toast(message,error=false){clearTimeout(toastTimer);notice.textContent=message;notice.className=error?'error':'ok';toastTimer=setTimeout(()=>notice.className='',4500)}
function searchable(w){return [w.display_title,w.default_title,w.platform,...w.editions.flatMap(e=>[e.display_title,e.default_title,e.edition_type,e.author,...(e.languages||[]),...(e.artifacts||[]).map(a=>a.path)])].join(' ').toLowerCase()}
function searchableSeries(s){return [s.display_title,s.default_title,s.description,...Object.values(s.titles||{}),...s.members.flatMap(m=>[m.game.display_title,m.game.default_title,m.game.platform,m.relation_type])].join(' ').toLowerCase()}
function findGame(id){return gameDetails.get(id)||allGames.find(w=>w.id===id)}
function findSeries(id){return allSeries.find(s=>s.id===id)}
function findEdition(id){
  for(const game of gameDetails.values()){const edition=game.editions.find(e=>e.id===id);if(edition)return {game,edition}}
  for(const game of allGames){if(gameDetails.has(game.id))continue;const edition=game.editions.find(e=>e.id===id);if(edition)return {game,edition}}
  return null;
}
async function loadGameDetail(id,{fresh=false}={}){
  if(!fresh&&gameDetails.has(id))return gameDetails.get(id);
  const game=await api(`/api/games/${encodeURIComponent(id)}?locale=${encodeURIComponent(locale.value)}`);gameDetails.set(id,game);return game;
}
async function loadEditionDetail(id){
  const found=findEdition(id);if(!found)return null;
  const game=await loadGameDetail(found.game.id,{fresh:true}),edition=game.editions.find(item=>item.id===id);return edition?{game,edition}:null;
}
function setNamed(form,name,value=''){form.elements.namedItem(name).value=value??''}
function resetMediaPicker(inputID,labelID){const input=$(`#${inputID}`),label=$(`#${labelID}`);if(input)input.value='';if(label)label.textContent=tr('未选择文件')}
function bindMediaPicker(inputID,labelID){const input=$(`#${inputID}`);if(input)input.onchange=()=>{$(`#${labelID}`).textContent=input.files?.[0]?.name||tr('未选择文件')}}

function render(){
  const q=search.value.trim().toLowerCase();
  if(libraryMode==='series'){
    const shown=q?allSeries.filter(s=>searchableSeries(s).includes(q)):allSeries,grouped=new Set(allSeries.flatMap(s=>s.members.map(m=>m.game_id))),ungrouped=allGames.filter(w=>!grouped.has(w.id)&&(!q||searchable(w).includes(q)));
    seriesEl.innerHTML=shown.map(seriesCard).join('')+(ungrouped.length?ungroupedCard(ungrouped):'');
    gamesEl.hidden=true;seriesEl.hidden=shown.length===0&&ungrouped.length===0;emptyEl.hidden=allGames.length!==0;noResults.hidden=allGames.length===0||shown.length+ungrouped.length!==0;
    resultCount.textContent=`${shown.length} 个系列 · ${ungrouped.length} 个未归组游戏`;$('#library-section-title').textContent='系列与成员';
  }else{
    const shown=q?allGames.filter(w=>searchable(w).includes(q)):allGames;
    const list=libraryMode==='list';gamesEl.className=list?'management-table':'grid cover-grid';gamesEl.innerHTML=list?managementTable(shown):shown.map(card).join('');gamesEl.hidden=shown.length===0;seriesEl.hidden=true;
    emptyEl.hidden=allGames.length!==0;noResults.hidden=allGames.length===0||shown.length!==0;resultCount.textContent=`${shown.length} / ${allGames.length}`;$('#library-section-title').textContent='全部游戏';
  }
  bindCards();bindSeriesCards();
  if(libraryMode==='list'||libraryMode==='covers')hydrateLibraryCovers();else clearLibraryCoverURLs();
}
async function load(){
  try{
    const activeGameID=gameDialog.open?editingGameID:editionDialog.open?findEdition(editingEditionID)?.game.id:'';
    [allGames,allSeries]=await Promise.all([api(`/api/games?projection=summary&locale=${encodeURIComponent(locale.value)}`),api(`/api/series?projection=summary&locale=${encodeURIComponent(locale.value)}`)]);gameDetails.clear();
    if(activeGameID&&allGames.some(game=>game.id===activeGameID))await loadGameDetail(activeGameID,{fresh:true});
    seriesCount.textContent=allSeries.length;gameCount.textContent=allGames.length;editionCount.textContent=allGames.reduce((n,w)=>n+w.editions.length,0);platformCount.textContent=new Set(allGames.map(w=>w.platform)).size;
    render();renderSyncStatus();renderSaveHistory();
  }catch(e){toast(e.message,true)}
}
function coverTitle(w){const title=w.display_title||w.default_title||'?';return title.split(/\s+/).filter(Boolean).slice(0,2).join(' ')}
function coverLabel(w){return w.display_title||w.default_title||'未命名游戏'}
function preferredCover(w){if(w.cover)return w.cover;const preferred=['cover','box_front','poster','tile','screenshot'];const media=[...(w.media||[]),...w.editions.flatMap(e=>e.media||[])];return preferred.map(kind=>media.find(item=>item.kind===kind&&item.mime_type?.startsWith('image/'))).find(Boolean)}
function editionArtifactSummary(e){
  if(e.artifact_stats)return {...e.artifact_stats,referenced:e.artifact_stats.total-e.artifact_stats.managed};
  const artifacts=e.artifacts||[],missing=artifacts.filter(a=>a.missing).length,hashed=artifacts.filter(a=>a.sha256).length,managed=artifacts.filter(a=>a.storage_kind==='managed').length;
  return {total:artifacts.length,missing,hashed,usable:artifacts.filter(a=>a.sha256&&!a.missing).length,managed,referenced:artifacts.length-managed};
}
function editionMeta(e){
  const bits=[];if(e.version)bits.push(`v${e.version}`);if(e.languages?.length)bits.push(e.languages.join(' · '));if(e.author)bits.push(e.author);
  const files=editionArtifactSummary(e);if(files.missing)bits.push(tr(`${files.missing} 个文件缺失`));else if(files.total)bits.push(tr(`${files.total} 个文件`));
  return bits.join('  /  ')||tr('独立存档空间');
}
function gameFileSummary(w){
  return w.editions.reduce((total,edition)=>{const item=editionArtifactSummary(edition);for(const key of ['total','missing','hashed','usable','managed','referenced'])total[key]+=item[key]||0;return total},{total:0,missing:0,hashed:0,usable:0,managed:0,referenced:0});
}
function gameSaveCount(w){const ids=new Set(w.editions.map(e=>e.id));return saveRevisions.filter(r=>ids.has(r.edition_id)).length}
function editionSaveSummary(e){const history=saveRevisions.filter(r=>r.edition_id===e.id);return {count:history.length,latest:history[0]||null,conflicts:history.filter(r=>r.status==='conflict'||r.conflict).length}}
function shortDate(value){if(!value)return '—';const tag=locale.value==='zh-TW'?'zh-TW':locale.value==='ja'?'ja-JP':locale.value==='en'?'en-US':'zh-CN';return new Intl.DateTimeFormat(tag,{year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(value))}
function managementTable(games){
  const header='<div class="management-head" role="row"><span>名称与系列</span><span>游戏平台</span><span>版本</span><span>ROM 与文件</span><span>存储</span><span>存档</span><span>更新时间</span><span aria-label="操作"></span></div>';
  return header+games.map(managementRow).join('');
}
function managementRow(w){
  const cover=preferredCover(w),memberships=allSeries.filter(s=>s.members.some(m=>m.game_id===w.id)),files=gameFileSummary(w),saves=gameSaveCount(w),types=[...new Set(w.editions.map(e=>e.edition_type))],storageText=[files.managed?`${files.managed} 受管`:'',files.referenced?`${files.referenced} 引用`:''].filter(Boolean).join(' · ')||'—';
  const fileState=!files.total?'<span class="health-state unlinked"><i></i>未关联 ROM</span>':files.missing?`<span class="health-state missing"><i></i>${files.missing} 个文件缺失</span>`:files.hashed<files.total?`<span class="health-state pending"><i></i>${files.total-files.hashed} 个待计算指纹</span>`:'<span class="health-state ready"><i></i>文件完整</span>';
  const editionTags=types.slice(0,3).map(type=>`<span class="edition-chip ${esc(type)}">${esc(tr(typeName[type]||type))}</span>`).join('');
  return `<article class="management-row" role="row"><div class="management-title" role="cell"><button class="list-cover game-detail" data-game="${esc(w.id)}" data-platform="${esc(w.platform.toLowerCase())}" aria-label="查看 ${esc(w.display_title)} 详情">${cover?`<img data-library-media="${esc(cover.id)}" alt="${esc(w.display_title)} 封面">`:''}<span>${esc(w.platform)}</span></button><span class="management-copy"><button class="game-detail" data-game="${esc(w.id)}">${esc(w.display_title)}</button><small>${w.display_title!==w.default_title?esc(w.default_title):memberships.map(s=>esc(s.display_title)).join(' · ')||'未加入系列'}</small></span></div><div class="management-platform" role="cell"><strong>${esc(w.platform)}</strong><small>${esc((locale.value==='en'?platformByValue(w.platform)?.name:platformByValue(w.platform)?.name_zh)||w.platform)}</small></div><div class="management-editions" role="cell"><strong>${w.editions.length} 个版本</strong><span>${editionTags||'<em>等待整理</em>'}</span></div><div class="management-files" role="cell">${fileState}<small>${files.total?`${files.total} 个文件 · ${files.hashed} 个指纹`:'添加版本后关联文件'}</small></div><div class="management-storage" role="cell"><strong>${storageText}</strong><small>${files.total?'文件位置':'尚无文件'}</small></div><div class="management-saves" role="cell"><strong>${saves?`${saves} 个快照`:'无存档'}</strong><small>${w.editions.length?`${w.editions.length} 个命名空间`:'等待版本'}</small></div><time role="cell" datetime="${esc(w.updated_at||'')}">${esc(shortDate(w.updated_at))}</time><div class="management-actions" role="cell"><button class="add-edition" data-id="${esc(w.id)}" aria-label="为 ${esc(w.display_title)} 添加版本">${uiIcon('add')}</button><button class="game-edit" data-game="${esc(w.id)}" aria-label="编辑 ${esc(w.display_title)}">${uiIcon('more')}</button></div></article>`;
}
function card(w){
  const sameTitle=w.display_title===w.default_title;
  const memberships=allSeries.filter(s=>s.members.some(m=>m.game_id===w.id));
  const cover=preferredCover(w),types=[...new Set(w.editions.map(e=>e.edition_type))],editions=w.editions.slice(0,3).map(e=>`<button class="tile-edition edition-edit ${w.primary_edition_id===e.id?'primary-edition':''}" data-edition="${esc(e.id)}" title="${esc(e.display_title)} · ${esc(editionMeta(e))}"><i class="${esc(e.edition_type)}"></i>${esc(tr(typeName[e.edition_type]||e.edition_type))}</button>`).join('');
  return `<article class="game-card library-game-card"><div class="game-art" data-platform="${esc(w.platform.toLowerCase())}">${cover?`<img data-library-media="${esc(cover.id)}" alt="${esc(w.display_title)} 封面">`:''}<div class="art-fallback"><span class="art-grid"></span><strong>${esc(coverLabel(w))}</strong><small>${esc(w.platform)}</small></div><div class="art-shade"></div><span class="art-platform">${esc(w.platform)}</span><span class="art-count" aria-label="${w.editions.length} 个版本">${w.editions.length}</span><button class="art-open game-detail" data-game="${esc(w.id)}" aria-label="查看 ${esc(w.display_title)} 详情"></button><button class="art-edit game-edit" data-game="${esc(w.id)}" aria-label="编辑 ${esc(w.display_title)}">${uiIcon('more')}</button></div><div class="game-tile-body"><div class="tile-series ${memberships.length?'':'is-empty'}">${memberships.slice(0,2).map(s=>`<button class="series-edit" data-series="${esc(s.id)}">${esc(s.display_title)}</button>`).join('')||'<span aria-hidden="true"></span>'}</div><button class="tile-title game-detail" data-game="${esc(w.id)}" title="${esc(w.display_title)}">${esc(w.display_title)}</button><p class="tile-subtitle">${sameTitle?(w.editions.length?`${w.editions.length} 个独立版本`:'尚未添加可运行版本'):esc(w.default_title)}</p>${w.editions.length?`<div class="tile-editions">${editions}${w.editions.length>3?`<span>+${w.editions.length-3}</span>`:''}</div>`:`<button class="tile-empty-version add-edition" data-id="${esc(w.id)}">${uiIcon('add')}<span>添加第一个版本</span></button>`}<footer><span class="tile-state"><i></i>${types.length?types.map(t=>tr(typeName[t]||t)).join(' · '):tr('等待整理')}</span>${w.editions.length?`<button class="tile-add add-edition" data-id="${esc(w.id)}" aria-label="为 ${esc(w.display_title)} 添加版本">${uiIcon('add')}</button>`:'<span class="tile-add-spacer"></span>'}</footer></div></article>`;
}
function clearLibraryCoverURLs(){libraryMediaObjectURLs.forEach(URL.revokeObjectURL);libraryMediaObjectURLs=[]}
function knownMediaMIME(id){for(const game of allGames){if(game.cover?.id===id)return game.cover.mime_type||'';const detail=gameDetails.get(game.id),item=[...(detail?.media||[]),...(detail?.editions||[]).flatMap(edition=>edition.media||[])].find(media=>media.id===id);if(item)return item.mime_type||''}return ''}
function normalizedMediaMIME(mimeType){return String(mimeType||'').trim().toLowerCase().split(';',1)[0]}
function canFallbackOriginalRaster(mimeType){const value=normalizedMediaMIME(mimeType);return value==='image/webp'||value==='image/avif'||value==='image/bmp'||value==='image/x-icon'||value==='image/vnd.microsoft.icon'}
function canRequestSafeThumbnail(mimeType){const value=normalizedMediaMIME(mimeType);return !value||value==='image/png'||value==='image/jpeg'||value==='image/gif'}
async function mediaImageBlob(id,size,mimeType){const token=sessionStorage.getItem('game-library-token'),headers=token?{Authorization:`Bearer ${token}`}:{},base=`/api/v1/media/${encodeURIComponent(id)}`,known=mimeType||knownMediaMIME(id);if(canFallbackOriginalRaster(known)){const response=await fetch(`${base}/content`,{headers});return response.ok?response.blob():null}if(!canRequestSafeThumbnail(known))return null;const response=await fetch(`${base}/thumbnail?size=${size}`,{headers});return response.ok?response.blob():null}
async function hydrateLibraryCovers(){clearLibraryCoverURLs();await Promise.all([...document.querySelectorAll('[data-library-media]')].map(async image=>{try{const blob=await mediaImageBlob(image.dataset.libraryMedia,480,image.dataset.libraryMediaMime);if(!blob)return;const url=URL.createObjectURL(blob);libraryMediaObjectURLs.push(url);image.onload=()=>image.classList.add('loaded');image.src=url}catch{}}))}
function seriesCard(s){
  const platforms=[...new Set(s.members.map(m=>m.game.platform))];
  const editions=s.members.reduce((sum,m)=>sum+m.game.editions.length,0);
  return `<details class="series-group" open><summary><span class="series-disclosure">${uiIcon('chevron')}</span><span class="series-summary-copy"><strong>${esc(s.display_title)}</strong><small>${esc(s.description||'跨平台系列')}</small></span><span class="series-platforms-compact">${platforms.map(p=>`<b>${esc(p)}</b>`).join('')}</span><span class="series-summary-count"><strong>${s.members.length}</strong><small>游戏</small></span><span class="series-summary-count"><strong>${editions}</strong><small>版本</small></span></summary><div class="series-group-body"><div class="series-group-tools"><span>${s.display_title!==s.default_title?esc(s.default_title):`${platforms.length} 个平台`}</span><button class="series-edit" data-series="${esc(s.id)}">编辑系列</button></div>${s.members.map((m,i)=>seriesMemberRow(m,i)).join('')||'<p class="series-no-member">还没有成员，编辑系列并勾选游戏。</p>'}</div></details>`;
}
function seriesMemberRow(member,index){const w=member.game,files=gameFileSummary(w);return `<div class="series-child-row"><i>${String(index+1).padStart(2,'0')}</i><button class="series-child-title game-detail" data-game="${esc(w.id)}"><strong>${esc(w.display_title)}</strong><small>${esc(w.platform)} · ${esc(tr(relationName[member.relation_type]||member.relation_type))}</small></button><span>${w.editions.length} 个版本</span><span class="health-state ${files.missing?'missing':files.total?'ready':'unlinked'}"><i></i>${files.missing?`${files.missing} 个文件缺失`:files.total?'文件完整':'未关联 ROM'}</span><button class="game-edit" data-game="${esc(w.id)}">编辑</button></div>`}
function ungroupedCard(games){return `<details class="series-group ungrouped-group" open><summary><span class="series-disclosure">${uiIcon('chevron')}</span><span class="series-summary-copy"><strong>未归组游戏</strong><small>尚未加入任何跨平台系列</small></span><span class="series-summary-count"><strong>${games.length}</strong><small>游戏</small></span></summary><div class="series-group-body"><div class="series-group-tools"><span>游戏数据保持独立完整</span><button class="series-create">建立新系列</button></div>${games.map((game,index)=>seriesMemberRow({game,relation_type:'other'},index)).join('')}</div></details>`}
function bindCards(){
  document.querySelectorAll('.add-edition').forEach(b=>b.onclick=()=>openNewEdition(b.dataset.id));
  document.querySelectorAll('.game-edit').forEach(b=>b.onclick=()=>openEditGame(b.dataset.game));
  document.querySelectorAll('.game-detail').forEach(b=>b.onclick=()=>openGameDetail(b.dataset.game));
  document.querySelectorAll('.edition-edit').forEach(b=>b.onclick=()=>openEditEdition(b.dataset.edition));
}
function bindSeriesCards(){document.querySelectorAll('.series-edit').forEach(b=>b.onclick=()=>openEditSeries(b.dataset.series));document.querySelectorAll('.series-create').forEach(b=>b.onclick=openNewSeries)}

function webPlayArtifact(game,edition){
  const platform=String(game?.platform||'').toLowerCase(),allowed=webPlayableExtensionsByPlatform.get(platform),minimum=webPlayableMinimumBytesByPlatform.get(platform)||1;
  return (edition.artifacts||[]).find(artifact=>{const role=artifact.role||'rom',extension=String(artifact.path||'').split('.').pop().toLowerCase();return !artifact.missing&&artifact.sha256&&artifact.size>=minimum&&artifact.size<=134217728&&['rom','disc','executable'].includes(role)&&allowed?.has(extension)})||null;
}
function invalidWebPlayArtifact(game,edition){
  const platform=String(game?.platform||'').toLowerCase(),allowed=webPlayableExtensionsByPlatform.get(platform),minimum=webPlayableMinimumBytesByPlatform.get(platform)||1;
  return (edition.artifacts||[]).find(artifact=>{const role=artifact.role||'rom',extension=String(artifact.path||'').split('.').pop().toLowerCase();return !artifact.missing&&artifact.sha256&&artifact.size>0&&artifact.size<minimum&&['rom','disc','executable'].includes(role)&&allowed?.has(extension)})||null;
}
function canOfferWebPlay(game,edition){return Boolean(capabilities?.features?.web_emulation&&webPlayablePlatforms.has(game.platform)&&webPlayArtifact(game,edition))}
function webNetplayArtifact(game,edition){const capability=(webNetplayReadiness?.platform_capabilities||[]).find(item=>item.platform_id===game?.platform);if(!capability)return null;const extensions=new Set((capability.extensions||[]).map(value=>String(value).toLowerCase().replace(/^\./,''))),minimum=Number(capability.minimum_rom_bytes)||1,maximum=Number(capability.maximum_rom_bytes)||134217728;return (edition?.artifacts||[]).find(artifact=>{const role=artifact.role||'rom',extension=String(artifact.path||'').split('.').pop().toLowerCase();return !artifact.missing&&artifact.sha256&&artifact.size>=minimum&&artifact.size<=maximum&&['rom','disc','executable'].includes(role)&&extensions.has(extension)})||null}
function canOfferWebNetplay(game,edition){return Boolean(capabilities?.features?.web_netplay&&webNetplayReadiness?.enabled&&webNetplayReadiness?.signal_ready&&webNetplayArtifact(game,edition))}
const webPlayerStateName={idle:'等待启动',loading:'正在准备',ready:'核心已就绪',starting:'正在启动',started:'运行中',timeout:'启动超时',error:'资源加载失败'};
const webNetplayStateName={opening:'正在建立房间',finding:'正在寻找房间',waiting:'等待另一位玩家',negotiating:'正在建立直连',connected:'联机已连接',error:'联机失败'};
function setWebPlayerState(state='idle'){const target=$('#web-player-runtime-state');if(!target)return;const value=Object.hasOwn(webPlayerStateName,state)?state:'error';target.dataset.state=value;target.textContent=tr(webPlayerStateName[value])}
function setWebPlayerInput(input={}){const target=$('#web-player-input-state');if(!target)return;let state='waiting',label='未检测到手柄';if(input.supported===false){state='unsupported';label='浏览器未开放手柄接口'}else if(input.count>0){state=input.standard===input.count?'connected':'custom';label=`${tr('手柄已连接')} · ${input.count}`};target.dataset.state=state;target.querySelector('b').textContent=tr(label)}
function setWebNetplayState(state='opening',players=0){const bar=$('#web-player-netplay'),target=$('#web-player-netplay-state');if(!bar||bar.hidden)return;const value=Object.hasOwn(webNetplayStateName,state)?state:'error';bar.dataset.state=value;target.textContent=tr(webNetplayStateName[value])+(players?` · ${players}/2`:'')}
function webNetplayClientID(){let value=sessionStorage.getItem('varkiv-web-netplay-client');if(value)return value;value=crypto.randomUUID?.()||`${Date.now()}-${Math.random().toString(16).slice(2)}`;sessionStorage.setItem('varkiv-web-netplay-client',value);return value}
function showWebPlayer(found,session){
  $('#web-player-title').textContent=found.edition.display_title;setWebPlayerState('loading');setWebPlayerInput({supported:true,count:0});
  const netplay=$('#web-player-netplay'),invite=$('#web-player-invite-code'),copy=$('#copy-web-player-invite');netplay.hidden=!session.role;if(session.role){netplay.dataset.role=session.role;invite.textContent=session.invite_code||'';invite.hidden=!session.invite_code;copy.hidden=!session.invite_code;copy.dataset.invite=session.invite_code||'';setWebNetplayState(session.role==='host'?'opening':'finding')}
  $('#web-player-frame').src=session.player_url;webPlayerDialog.showModal();
}
async function openWebPlayer(editionID,button){
  button.disabled=true;
  try{
    const found=findEdition(editionID);if(!found)return;
    const session=await api('/api/web-emulation/sessions',{method:'POST',body:JSON.stringify({edition_id:editionID,locale:locale.value})});
    gameDetailDialog.close();showWebPlayer(found,session);
  }catch(error){toast(error.message,true)}finally{button.disabled=false}
}
function openWebNetplay(editionID){const found=findEdition(editionID);if(!found)return;const form=$('#web-netplay-form');form.reset();form.dataset.edition=editionID;form.elements.display_name.value=tr('玩家');$('#web-netplay-edition-title').textContent=found.edition.display_title;syncWebNetplayMode();webNetplayDialog.showModal()}
function syncWebNetplayMode(){const form=$('#web-netplay-form'),guest=form.elements.mode.value==='guest';$('#web-netplay-invite-field').hidden=!guest;form.elements.invite_code.required=guest;form.querySelector('button[type="submit"] span').textContent=tr(guest?'验证并加入':'创建并运行')}
async function submitWebNetplay(event){event.preventDefault();const form=event.currentTarget,found=findEdition(form.dataset.edition);if(!found)return;const data=Object.fromEntries(new FormData(form)),guest=data.mode==='guest',button=event.submitter;button.disabled=true;try{const body={edition_id:found.edition.id,locale:locale.value,client_id:webNetplayClientID(),display_name:data.display_name};if(guest)body.invite_code=data.invite_code.trim();const session=await api(guest?'/api/web-netplay/sessions/join':'/api/web-netplay/sessions',{method:'POST',body:JSON.stringify(body)});webNetplayDialog.close();gameDetailDialog.close();showWebPlayer(found,session)}catch(error){toast(error.message,true)}finally{button.disabled=false}}
async function copyWebNetplayInvite(){const value=$('#copy-web-player-invite').dataset.invite;if(!value)return;try{await navigator.clipboard.writeText(value);toast(tr('邀请口令已复制'))}catch{toast(tr('无法复制，请手动记录邀请口令'),true)}}
function closeWebPlayer(){const frame=$('#web-player-frame'),invite=$('#web-player-invite-code'),copy=$('#copy-web-player-invite');frame.src='about:blank';setWebPlayerState('idle');setWebPlayerInput({supported:true,count:0});$('#web-player-netplay').hidden=true;invite.textContent='';invite.hidden=true;copy.dataset.invite='';copy.hidden=true;if(webPlayerDialog.open)webPlayerDialog.close()}
async function openGameDetail(id){
  let w;try{w=await loadGameDetail(id,{fresh:true})}catch(error){toast(error.message,true);return}if(!w)return;
  const cover=preferredCover(w),memberships=allSeries.filter(s=>s.members.some(m=>m.game_id===w.id)),platform=platformByValue(w.platform),files=w.editions.reduce((n,e)=>n+(e.artifacts?.length||0),0);
  const editionRows=w.editions.map(e=>{
    const saves=editionSaveSummary(e),single=canOfferWebPlay(w,e)?`<button class="detail-play" data-web-play="${esc(e.id)}" title="${esc(tr('在浏览器运行这个版本'))}">${uiIcon('play')}<span>${esc(tr('浏览器运行'))}</span></button>`:capabilities?.features?.web_emulation&&webPlayablePlatforms.has(w.platform)&&invalidWebPlayArtifact(w,e)?`<span class="detail-play-unavailable" title="${esc(tr('文件不是可识别的 ROM'))}" aria-label="${esc(tr('文件不是可识别的 ROM'))}">${uiIcon('warning')}<span>${esc(tr('ROM 无效'))}</span></span>`:'',online=canOfferWebNetplay(w,e)?`<button class="detail-play detail-netplay" data-web-netplay="${esc(e.id)}" title="${esc(tr('与另一台浏览器联机'))}">${uiIcon('network')}<span>${esc(tr('网页联机'))}</span></button>`:'';
    return `<div class="detail-edition-item"><button class="detail-edition-row" data-edition="${esc(e.id)}"><span class="detail-edition-type ${esc(e.edition_type)}">${esc(tr(typeName[e.edition_type]||e.edition_type))}</span><span><strong>${esc(e.display_title)}</strong><small>${esc(editionMeta(e))}</small></span><span class="detail-save-state"><strong>${esc(saves.count?tr(`${saves.count} 个快照`):tr('尚无存档'))}</strong><small>${esc(saves.latest?tr(`最近同步 ${shortDate(saves.latest.created_at)}`):tr(`命名空间 ${e.save_namespace.slice(0,8)}`))}</small></span><b aria-hidden="true">${uiIcon('arrow')}</b></button><div class="detail-play-actions">${single}${online}</div></div>`;
  }).join('');
  $('#game-detail-content').innerHTML=`<section class="detail-hero"><div class="detail-art" data-platform="${esc(w.platform.toLowerCase())}">${cover?`<img data-detail-media="${esc(cover.id)}" alt="${esc(w.display_title)} 封面">`:''}<div class="art-fallback"><span class="art-grid"></span><strong>${esc(coverLabel(w))}</strong><small>${esc(w.platform)}</small></div></div><div class="detail-copy"><div class="detail-kicker"><span>${esc((locale.value==='en'?platform?.name:platform?.name_zh)||platform?.name||w.platform)}</span><i></i><b>${esc(w.platform)}</b></div><h2>${esc(w.display_title)}</h2>${w.display_title!==w.default_title?`<p class="detail-original">${esc(w.default_title)}</p>`:''}<div class="detail-series">${memberships.length?memberships.map(s=>`<button class="series-edit" data-series="${esc(s.id)}">${esc(s.display_title)}</button>`).join(''):'<span>未加入系列</span>'}</div><p class="detail-boundary">这是 ${esc(w.platform)} 平台上的独立游戏；下列版本各自保留 ROM、媒体和存档命名空间。</p><div class="detail-stats"><div><strong>${w.editions.length}</strong><span>独立版本</span></div><div><strong>${files}</strong><span>关联文件</span></div><div><strong>${gameSaveCount(w)}</strong><span>存档快照</span></div></div><div class="detail-actions"><button class="primary detail-add-edition">${uiIcon('add')}<span>添加版本</span></button><button class="detail-edit-game">编辑游戏资料</button></div></div></section><section class="detail-editions"><header><div><p>版本</p><h3>版本、ROM 与存档</h3></div><span>${w.editions.length?`${w.editions.length} 个可独立维护的版本`:'尚未建立版本'}</span></header><div>${editionRows||`<button class="detail-empty detail-add-edition"><strong>添加第一个可运行版本</strong><span>原版、汉化版和改版会分别保存 ROM 与存档。</span></button>`}</div></section>`;
  gameDetailDialog.showModal();
  $('#game-detail-content').querySelectorAll('.detail-add-edition').forEach(b=>b.onclick=()=>{gameDetailDialog.close();openNewEdition(w.id)});
  $('#game-detail-content').querySelector('.detail-edit-game').onclick=()=>{gameDetailDialog.close();openEditGame(w.id)};
  $('#game-detail-content').querySelectorAll('.detail-edition-row').forEach(b=>b.onclick=()=>{gameDetailDialog.close();openEditEdition(b.dataset.edition)});
  $('#game-detail-content').querySelectorAll('[data-web-play]').forEach(b=>b.onclick=()=>openWebPlayer(b.dataset.webPlay,b));
  $('#game-detail-content').querySelectorAll('[data-web-netplay]').forEach(b=>b.onclick=()=>openWebNetplay(b.dataset.webNetplay));
  $('#game-detail-content').querySelectorAll('.series-edit').forEach(b=>b.onclick=()=>{gameDetailDialog.close();openEditSeries(b.dataset.series)});
  hydrateDetailCover();
}
async function hydrateDetailCover(){const image=document.querySelector('[data-detail-media]');if(!image)return;try{const blob=await mediaImageBlob(image.dataset.detailMedia,768,image.dataset.detailMediaMime);if(!blob)return;const url=URL.createObjectURL(blob);image.onload=()=>image.classList.add('loaded');image.src=url;gameDetailDialog.addEventListener('close',()=>URL.revokeObjectURL(url),{once:true})}catch{}}

function openNewSeries(){
  editingSeriesID='';seriesForm.reset();$('#series-dialog-kicker').textContent='NEW SERIES';$('#series-dialog-title').textContent='建立跨平台系列';$('#series-manage').hidden=true;renderSeriesMemberEditor(null);seriesDialog.showModal();
}
function openEditSeries(id){
  const series=findSeries(id);if(!series)return;editingSeriesID=id;seriesForm.reset();setNamed(seriesForm,'default_title',series.default_title);setNamed(seriesForm,'description',series.description);
  ['zh-CN','zh-TW','ja','en'].forEach(l=>setNamed(seriesForm,`s-${l}`,series.titles?.[l]||''));$('#series-dialog-kicker').textContent='EDIT SERIES';$('#series-dialog-title').textContent='编辑系列与成员';$('#series-manage').hidden=false;renderSeriesMemberEditor(series);seriesDialog.showModal();
}
function renderSeriesMemberEditor(series){
  const members=new Map((series?.members||[]).map(m=>[m.game_id,m]));
  $('#series-member-editor').innerHTML=allGames.map(w=>{const member=members.get(w.id),checked=!!member;return `<div class="series-member-option ${checked?'selected':''}" data-series-member="${esc(w.id)}"><label><input type="checkbox" value="${esc(w.id)}" ${checked?'checked':''}><span class="member-platform">${esc(w.platform)}</span><span><strong>${esc(w.display_title)}</strong><small>${w.editions.length} 个独立版本</small></span></label><select aria-label="成员关系" ${checked?'':'disabled'}>${Object.entries(relationName).map(([value,name])=>`<option value="${value}" ${member?.relation_type===value?'selected':''}>${name}</option>`).join('')}</select><input class="member-order" type="number" min="0" step="1" aria-label="排序" value="${member?.sort_order??((allGames.indexOf(w)+1)*10)}" ${checked?'':'disabled'}></div>`}).join('')||'<p class="muted">请先建立或导入游戏。</p>';
  document.querySelectorAll('[data-series-member] input[type="checkbox"]').forEach(input=>input.onchange=()=>{const row=input.closest('[data-series-member]');row.classList.toggle('selected',input.checked);row.querySelectorAll('select,.member-order').forEach(control=>control.disabled=!input.checked);updateSeriesMemberCount()});updateSeriesMemberCount();
}
function updateSeriesMemberCount(){const count=document.querySelectorAll('[data-series-member] input[type="checkbox"]:checked').length;$('#series-member-count').textContent=`${count} 项`}
seriesForm.onsubmit=async e=>{
  e.preventDefault();const f=new FormData(e.target),titles={};['zh-CN','zh-TW','ja','en'].forEach(l=>{const value=f.get(`s-${l}`);if(value)titles[l]=value});const button=e.submitter;
  const selected=[...document.querySelectorAll('[data-series-member]')].filter(row=>row.querySelector('input[type="checkbox"]').checked),members=[];
  for(const row of selected){const order=Number(row.querySelector('.member-order').value);if(!Number.isInteger(order)||order<0){toast(tr('排序必须是零或正整数'),true);return}members.push({game_id:row.dataset.seriesMember,relation_type:row.querySelector('select').value,sort_order:order})}
  button.disabled=true;
  try{
    await api(editingSeriesID?`/api/series/${editingSeriesID}`:'/api/series',{method:editingSeriesID?'PUT':'POST',body:JSON.stringify({default_title:f.get('default_title'),description:f.get('description'),titles,members})});seriesDialog.close();toast(editingSeriesID?'系列与成员已更新':'跨平台系列已创建');await load();setLibraryMode('series');
  }catch(err){toast(err.message,true)}finally{button.disabled=false}
};
$('#delete-series').onclick=async()=>{const series=findSeries(editingSeriesID);if(!series||!confirm(`删除系列“${series.display_title}”？游戏、版本、ROM 与存档都不会删除。`))return;try{await api(`/api/series/${series.id}`,{method:'DELETE'});seriesDialog.close();toast('系列分组已删除，游戏数据保持不变');await load()}catch(err){toast(err.message,true)}};

function setLibraryMode(mode){libraryMode=['list','covers','series'].includes(mode)?mode:'list';document.querySelectorAll('[data-library-mode]').forEach(button=>{const active=button.dataset.libraryMode===libraryMode;button.classList.toggle('active',active);button.setAttribute('aria-pressed',String(active))});const action=$('#new-game');action.innerHTML=`${uiIcon('add')}${libraryMode==='series'?tr('新建系列'):tr('添加游戏')}`;render()}
document.querySelectorAll('[data-library-mode]').forEach(button=>button.onclick=()=>setLibraryMode(button.dataset.libraryMode));

function openNewGame(){
  editingGameID='';gameForm.reset();setPlatformChoice('game','');$('#game-dialog-kicker').textContent='NEW GAME';$('#game-dialog-title').textContent='添加游戏';$('#game-media-panel').hidden=true;$('#game-manage').hidden=true;gameDialog.showModal();
}
async function openEditGame(id){
  let w;try{w=await loadGameDetail(id,{fresh:true})}catch(error){toast(error.message,true);return}if(!w)return;
  editingGameID=id;gameForm.reset();setNamed(gameForm,'default_title',w.default_title);setPlatformChoice('game',w.platform);
  ['zh-CN','zh-TW','ja','en'].forEach(l=>setNamed(gameForm,l,w.titles?.[l]||''));
  $('#game-dialog-kicker').textContent='EDIT GAME';$('#game-dialog-title').textContent='编辑游戏';$('#game-media-panel').hidden=false;$('#game-manage').hidden=false;resetMediaPicker('game-media-file','game-media-file-name');renderGameMedia(w);
  const options=allGames.filter(x=>x.id!==id&&x.platform===w.platform);$('#merge-source').innerHTML=options.map(x=>`<option value="${esc(x.id)}">${esc(x.display_title)} · ${esc(tr(`${x.editions.length} 个版本`))}</option>`).join('')||`<option value="">${tr('没有同平台的其他游戏')}</option>`;$('#merge-game').disabled=options.length===0;
  gameDialog.showModal();
}
function openNewEdition(gameID){
  editingEditionID='';editionForm.reset();setNamed(editionForm,'game_id',gameID);$('#edition-dialog-kicker').textContent='NEW EDITION';$('#edition-dialog-title').textContent='添加独立版本';$('#edition-manage').hidden=true;$('#artifact-field').hidden=false;editionForm.querySelector(':scope > footer .primary').textContent='添加版本';editionDialog.showModal();
}
async function openEditEdition(id){
  let found;try{found=await loadEditionDetail(id)}catch(error){toast(error.message,true);return}if(!found)return;const {game,edition:e}=found;
  editingEditionID=id;editionForm.reset();setNamed(editionForm,'game_id',game.id);setNamed(editionForm,'default_title',e.default_title);setNamed(editionForm,'edition_type',e.edition_type);setNamed(editionForm,'version',e.version);editionForm.querySelectorAll('input[name="languages"]').forEach(input=>input.checked=(e.languages||[]).includes(input.value));setNamed(editionForm,'author',e.author);
  ['zh-CN','zh-TW','ja','en'].forEach(l=>setNamed(editionForm,`e-${l}`,e.titles?.[l]||''));
  $('#edition-dialog-kicker').textContent='EDIT EDITION';$('#edition-dialog-title').textContent='编辑独立版本';$('#edition-manage').hidden=false;$('#artifact-field').hidden=true;editionForm.querySelector(':scope > footer .primary').textContent='保存版本';resetMediaPicker('media-file','media-file-name');
  const targets=allGames.filter(x=>x.id!==game.id&&x.platform===game.platform);$('#move-target').innerHTML=targets.map(x=>`<option value="${esc(x.id)}">${esc(x.display_title)}</option>`).join('')||'<option value="">没有可移动的同平台游戏</option>';$('#move-edition').disabled=targets.length===0;$('#primary-edition').disabled=game.primary_edition_id===id;
  renderEditionFiles(e);
	renderEditionMedia(e);
	renderEditionLaunchBindings(e);
	renderRuntimeImportHints(e);
	renderEditionSaveBindings(e);
  editionDialog.showModal();
}
function renderEditionFiles(edition){
  $('#edition-files').innerHTML=(edition.artifacts||[]).map(a=>`<div class="artifact-row"><span class="artifact-kind">${esc(tr(artifactRoleName[a.role]||a.role))}${a.disc_index?` ${a.disc_index}`:''}</span><span><strong>${esc(a.path)}</strong><small>${a.missing?'文件缺失':`${(a.size/1024/1024).toFixed(2)} MB${a.sha256?' · '+a.sha256.slice(0,10):''}`}</small></span><span class="artifact-actions"><button type="button" class="edit-artifact" data-artifact="${esc(a.id)}">编辑</button><button type="button" class="remove-artifact" data-artifact="${esc(a.id)}">移除</button></span><div class="artifact-edit-panel" hidden><label><span>资源类型</span><select class="artifact-edit-role">${Object.entries(artifactRoleName).map(([value,label])=>`<option value="${esc(value)}"${value===a.role?' selected':''}>${esc(tr(label))}</option>`).join('')}</select></label><label><span>碟号</span><input class="artifact-edit-disc" type="number" min="0" max="64" value="${a.disc_index||0}"></label><small>只修改资料语义；ROM 文件位置和内容不会改变。</small><span><button type="button" class="cancel-artifact-edit">取消</button><button type="button" class="save-artifact-edit primary" data-artifact="${esc(a.id)}">保存文件语义</button></span></div></div>`).join('')||'<p class="muted">尚未关联文件或目录</p>';
  document.querySelectorAll('.edit-artifact').forEach(button=>button.onclick=()=>{button.closest('.artifact-row').querySelector('.artifact-edit-panel').hidden=false});
  document.querySelectorAll('.cancel-artifact-edit').forEach(button=>button.onclick=()=>{button.closest('.artifact-edit-panel').hidden=true});
  document.querySelectorAll('.save-artifact-edit').forEach(button=>button.onclick=()=>updateArtifact(button.dataset.artifact,button));
  document.querySelectorAll('.remove-artifact').forEach(button=>button.onclick=()=>removeArtifact(button.dataset.artifact));
}
function renderEditionMedia(edition){
  const media=edition.media||[];
  mediaObjectURLs.forEach(URL.revokeObjectURL);mediaObjectURLs=[];
  $('#edition-media').innerHTML=mediaRows(media,'edition');
  bindMediaEditors('#edition-media','edition');
  document.querySelectorAll('.remove-media').forEach(button=>button.onclick=()=>removeMedia(button.dataset.media));
  hydrateMediaImages();
}
function renderGameMedia(game){
  const media=game.media||[];
  mediaObjectURLs.forEach(URL.revokeObjectURL);mediaObjectURLs=[];
  $('#game-media').innerHTML=mediaRows(media,'game');
  bindMediaEditors('#game-media','game');
  document.querySelectorAll('.remove-game-media').forEach(button=>button.onclick=()=>removeGameMedia(button.dataset.media));
  hydrateMediaImages();
}
function mediaRows(media,scope){return media.map(item=>{const locales={...mediaLocaleName},status=item.content_status||'unverified';if(item.locale&&!Object.hasOwn(locales,item.locale))locales[item.locale]=item.locale;return `<div class="media-row media-status-${esc(status)}">${item.mime_type?.startsWith('image/')?`<img data-media-image="${esc(item.id)}" alt="">`:`<span class="media-type">${esc(item.kind.slice(0,3))}</span>`}<span><strong>${esc(item.original_name)}</strong><small>${esc(tr(mediaKindName[item.kind]||item.kind))} · ${(item.size/1024).toFixed(1)} KB · ${esc(item.storage_kind)}${item.locale?` · ${esc(item.locale)}`:''} · <b class="media-content-status">${esc(tr(mediaStatusName[status]||status))}</b></small></span><span class="media-actions"><button type="button" class="edit-media-meta">${tr('编辑')}</button><button type="button" class="${scope==='game'?'remove-game-media':'remove-media'}" data-media="${esc(item.id)}">${tr('移除')}</button></span><div class="media-edit-panel" hidden><label><span>${tr('媒体类型')}</span><select class="media-edit-kind">${Object.entries(mediaKindName).map(([value,label])=>`<option value="${esc(value)}"${value===item.kind?' selected':''}>${esc(tr(label))}</option>`).join('')}</select></label><label><span>${tr('媒体语言')}</span><select class="media-edit-locale">${Object.entries(locales).map(([value,label])=>`<option value="${esc(value)}"${value===item.locale?' selected':''}>${esc(tr(label))}</option>`).join('')}</select></label><label><span>${tr('排序')}</span><input class="media-edit-order" type="number" min="0" value="${item.sort_order||0}"></label><small>${tr('只修改媒体分类、语言与排序；归属对象、文件位置、哈希和内容不会改变。')}</small><span><button type="button" class="cancel-media-edit">${tr('取消')}</button><button type="button" class="save-media-edit primary" data-media="${esc(item.id)}">${tr('保存媒体信息')}</button></span></div></div>`}).join('')||`<p class="muted">${tr(scope==='game'?'尚未添加共享媒体':'尚未添加版本媒体')}</p>`}
function bindMediaEditors(container,scope){document.querySelectorAll(`${container} .edit-media-meta`).forEach(button=>button.onclick=()=>{const row=button.closest('.media-row');row.classList.add('editing');row.querySelector('.media-edit-panel').hidden=false});document.querySelectorAll(`${container} .cancel-media-edit`).forEach(button=>button.onclick=()=>{const row=button.closest('.media-row');row.classList.remove('editing');row.querySelector('.media-edit-panel').hidden=true});document.querySelectorAll(`${container} .save-media-edit`).forEach(button=>button.onclick=()=>updateMediaMetadata(button.dataset.media,button,scope))}
const supportLevelName={'catalogued':'仅已收录','package-tested':'软件包已验证','hardware-tested':'真机已验证','sync-tested':'存档同步已验证'};
const evidenceScopeName={fixture:'软件夹具',package:'软件包',hardware:'真实设备',sync:'真实存档同步',catalog:'目录收录',registry:'目录收录',user:'用户声明','android-emulator':'Android 模拟器','real-android-emulator':'Android 模拟器实测','real-browser':'真实浏览器','cross-runtime':'跨运行时往返','isolated-roundtrip':'隔离往返'};
function evidenceSummary(item){
  return tr({
    'catalogued':'仅登记了声明式兼容能力；尚未验证软件包或真实设备。',
    'package-tested':'已验证生成的软件包或配置夹具；这不代表真机运行或真实存档同步。',
    'hardware-tested':'已在记录的真实设备上验证运行；存档同步仍需单独达到更高等级。',
    'sync-tested':'已在记录的真实设备上完成自动存档同步往返。'
  }[item.support_level]||'尚未记录可核验的支持证据。');
}
function renderRuntimeEvidence(item){
  const section=$('#runtime-evidence');if(!item){section.hidden=true;return}section.hidden=false;
  const evidence=item.evidence||{},sources=Array.isArray(evidence.sources)?evidence.sources.filter(value=>/^https?:\/\//i.test(String(value))):[],targetClaims=Array.isArray(evidence.target_claims)?evidence.target_claims.filter(claim=>claim&&claim.target):[];
  $('#runtime-evidence-level').textContent=tr(supportLevelName[item.support_level]||item.support_level||'仅已收录');
  $('#runtime-evidence-contract').textContent=`${tr('契约版本')} v${item.contract_version||1}`;
  $('#runtime-evidence-scope').textContent=tr(evidenceScopeName[evidence.scope]||'未记录');
  $('#runtime-evidence-date').textContent=String(evidence.verified_at||evidence.tested_at||evidence.date||tr('未记录'));
  const evidenceDevice=String(evidence.device||''),evidenceVersion=String(evidence.software_version||''),evidenceScenarios=Array.isArray(evidence.scenarios)?evidence.scenarios.map(String).filter(Boolean):[];
  $('#runtime-evidence-device-row').hidden=!evidenceDevice;$('#runtime-evidence-device').textContent=evidenceDevice;
  $('#runtime-evidence-version-row').hidden=!evidenceVersion;$('#runtime-evidence-version').textContent=evidenceVersion;
  $('#runtime-evidence-scenarios-row').hidden=!evidenceScenarios.length;$('#runtime-evidence-scenarios').textContent=evidenceScenarios.join(' · ');
  const targets=$('#runtime-evidence-targets');targets.hidden=!targetClaims.length;targets.querySelector('div').innerHTML=targetClaims.map(claim=>{const scenarios=Array.isArray(claim.scenarios)?claim.scenarios.map(value=>acceptanceScenarioLabel(String(value))).filter(Boolean):[];return `<article><header><strong>${esc(claim.target)}</strong><em class="support-level ${esc(claim.support_level||'catalogued')}">${esc(tr(supportLevelName[claim.support_level]||claim.support_level||'仅已收录'))}</em></header><small>${esc([claim.verified_at,claim.software_version].filter(Boolean).join(' · '))}</small>${scenarios.length?`<p>${scenarios.map(value=>`<span>${esc(value)}</span>`).join('')}</p>`:''}</article>`}).join('');
  $('#runtime-evidence-summary').textContent=evidenceSummary(item);
  const details=$('#runtime-evidence-sources');details.hidden=!sources.length;details.querySelector('ul').innerHTML=sources.map(value=>{let label=value;try{label=new URL(value).hostname}catch{}return `<li><a href="${esc(value)}" target="_blank" rel="noreferrer noopener">${esc(label)}</a></li>`}).join('');
}
function runtimeCategoryIcon(kind){
  return `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><use href="#icon-runtime-${kind}"></use></svg>`;
}
function uiIcon(name){return `<svg viewBox="0 0 24 24" aria-hidden="true" focusable="false"><use href="#icon-${name}"></use></svg>`}
function renderRuntimeCatalog(){
  if(!$('#runtime-catalog'))return;const total=sourceAdapters.length+frontendAdapters.length+deviceProfiles.length+emulatorDrivers.length+retroarchCores.length;$('#runtime-total').textContent=total;
  const items=(kind,values,meta)=>values.map(item=>`<li><button type="button" data-runtime-kind="${kind}" data-runtime-id="${esc(item.id)}"><span><strong>${esc(item.name)}</strong><small>${esc(meta(item))}</small></span><span class="runtime-item-state"><em class="support-level ${esc(item.support_level)}">${esc(tr(supportLevelName[item.support_level]||item.support_level))}</em><i>${item.builtin?tr('内置'):tr('自定义')}</i></span></button></li>`).join('');
  $('#runtime-catalog').innerHTML=`<article><header><span>${runtimeCategoryIcon('source')}</span><div><strong>${tr('来源适配器')}</strong><small>${tr(`${sourceAdapters.length} 项`)}</small></div></header><ul>${items('source',sourceAdapters,x=>`${x.format} · ${x.handler}`)}</ul></article><article><header><span>${runtimeCategoryIcon('frontend')}</span><div><strong>${tr('前端适配器')}</strong><small>${tr(`${frontendAdapters.length} 项`)}</small></div></header><ul>${items('frontend',frontendAdapters,x=>`${x.format} · ${x.handler||tr('未绑定导出器')} · contract v${x.contract_version}`)}</ul></article><article><header><span>${runtimeCategoryIcon('device')}</span><div><strong>${tr('掌机目标')}</strong><small>${tr(`${deviceProfiles.length} 项`)}</small></div></header><ul>${items('device',deviceProfiles,x=>`${x.target} · ${x.path_style}`)}</ul></article><article><header><span>${runtimeCategoryIcon('driver')}</span><div><strong>${tr('模拟器驱动')}</strong><small>${tr(`${emulatorDrivers.length} 项`)}</small></div></header><ul>${items('driver',emulatorDrivers,x=>`${tr(`${x.platforms.length} 个平台`)} · ${tr(`${x.targets.length} 类设备`)}`)}</ul></article><article><header><span>${runtimeCategoryIcon('core')}</span><div><strong>${tr('RetroArch 核心')}</strong><small>${tr(`${retroarchCores.length} 项`)} · ${tr(`${coreMappings.length} 条映射`)}</small></div></header><ul>${items('core',retroarchCores,x=>x.platforms.join(' · '))}</ul></article>`;
  document.querySelectorAll('[data-runtime-kind]').forEach(button=>button.onclick=()=>openRuntimeEditor(button.dataset.runtimeKind,button.dataset.runtimeId));
}

const readinessGateNames={
  'windows-retroarch-sync':'Windows + RetroArch 自动同步','steamos-bazzite-hardware':'SteamOS / Bazzite 真机运行',
  'handheld-linux-hardware':'掌机 Linux 真机运行','android-boundaries-hardware':'Android 权限与启动边界'
};
const readinessGateIconKeys={'windows-retroarch-sync':'windows','steamos-bazzite-hardware':'handheld_linux','handheld-linux-hardware':'handheld_linux','android-boundaries-hardware':'android'};
function readinessGateIcon(id){return platformTargetIcon(readinessGateIconKeys[id]||'handheld_linux')}
const readinessMissingNames={device:'设备环境',frontend:'前端适配器',driver:'模拟器驱动',retroarch_core:'RetroArch 核心',one_handheld_linux_target:'至少一个掌机 Linux 目标',retroarch_driver:'RetroArch 驱动',ppsspp_driver:'PPSSPP 驱动'};
function renderSupportReadiness(){
  const state=$('#support-readiness-state'),grid=$('#support-readiness-grid');if(!state||!grid)return;
  if(!supportReadiness){state.className='';state.textContent=tr('正在检查证据…');grid.innerHTML='';return}
  if(supportReadiness.error){state.className='invalid';state.textContent=tr('支持证据审计失败');grid.innerHTML=`<article class="support-gate"><header><span>${runtimeCategoryIcon('source')}</span><em>${tr('需要检查')}</em></header><h3>${tr('支持证据需要检查')}</h3><p>${tr('当前证据无法通过合同校验；请检查运行目录或使用只读发布审计。')}</p></article>`;return}
  state.className=supportReadiness.ready?'ready':'';state.textContent=tr(supportReadiness.ready?'四条真实设备门禁均已满足':'仍有真实设备门禁待完成');
  grid.innerHTML=(supportReadiness.gates||[]).map(gate=>{const passed=gate.status==='passed',components=passed?((gate.satisfied_targets||[]).length?gate.satisfied_targets:['目标已验证']):(gate.missing||[]).map(value=>readinessMissingNames[value]||value),level=tr(supportLevelName[gate.required_level]||gate.required_level);return `<article class="support-gate ${passed?'passed':'pending'}"><header><span>${readinessGateIcon(gate.id)}</span><em>${tr(passed?'已满足':'待完成')}</em></header><h3>${esc(tr(readinessGateNames[gate.id]||gate.id))}</h3><p>${tr('要求等级')} · ${esc(level)}</p><div class="support-gate-components">${components.map(value=>`<span>${esc(tr(value))}</span>`).join('')}</div></article>`}).join('');
}
async function loadSupportReadiness(){try{supportReadiness=await api('/api/support-readiness')}catch{supportReadiness={error:true}}renderSupportReadiness()}

function renderWebEmulatorReadiness(){
  const section=$('#web-emulator-readiness'),title=$('#web-emulator-readiness-title'),meta=$('#web-emulator-readiness-meta'),state=$('#web-emulator-readiness-state');if(!section||!title||!meta||!state)return;
  const readiness=webEmulatorReadiness||{mode:'loading'};section.dataset.mode=readiness.mode;state.className='';
  if(readiness.mode==='self-hosted-verified'){title.textContent=tr('同源资源已核验');meta.textContent=`EmulatorJS ${readiness.emulatorjs_version} · ${tr(`${readiness.assets_verified} 个文件`)} · ${tr(`${readiness.supported_platforms?.length||0} 个平台`)}`;state.textContent=tr('已核验');state.className='verified';return}
  if(readiness.mode==='external-unverified'){title.textContent=tr(readiness.same_origin?'同源代理资源未核验':'外部资源未核验');meta.textContent=tr('浏览器运行可用；资源身份未由服务核验。');state.textContent=tr('未核验');state.className='unverified';return}
  if(readiness.mode==='disabled'){title.textContent=tr('浏览器运行未启用');meta.textContent=tr('复杂平台继续由设备上的原生模拟器运行。');state.textContent=tr('已关闭');return}
  title.textContent=tr('正在检查资源…');meta.textContent='';state.textContent=tr('检查中');
}
function syncWebEmulatorCapabilities(readiness){
  webPlayablePlatforms.clear();webPlayableExtensions.clear();webPlayableExtensionsByPlatform.clear();webPlayableMinimumBytesByPlatform.clear();
  for(const capability of readiness?.platform_capabilities||[]){
    const platform=String(capability?.platform_id||'').trim().toLowerCase(),extensions=new Set();if(!platform)continue;
    for(const value of capability?.extensions||[]){const extension=String(value).trim().toLowerCase().replace(/^\./,'');if(extension){extensions.add(extension);webPlayableExtensions.add(extension)}}
    const minimum=Number(capability?.minimum_rom_bytes);if(extensions.size&&Number.isSafeInteger(minimum)&&minimum>0){webPlayablePlatforms.add(platform);webPlayableExtensionsByPlatform.set(platform,extensions);webPlayableMinimumBytesByPlatform.set(platform,minimum)}
  }
}
async function loadWebEmulatorReadiness(){try{webEmulatorReadiness=await api('/api/web-emulation/readiness')}catch{webEmulatorReadiness={mode:'disabled',enabled:false,supported_platforms:[],supported_extensions:[],platform_capabilities:[]}}syncWebEmulatorCapabilities(webEmulatorReadiness);renderWebEmulatorReadiness();if(platformPresets.length)renderPlatformCatalog()}
async function loadWebNetplayReadiness(){try{webNetplayReadiness=await api('/api/web-netplay/readiness')}catch{webNetplayReadiness={enabled:false,signal_ready:false,supported_platforms:[]}}if(allGames.length)render()}

const acceptanceScenarioNames={
  'frontend-launch':'前端启动','rom-launch':'ROM 启动','emulator-exit':'模拟器退出','save-created':'生成存档',
  'sync-upload':'上传存档','sync-download':'下载存档','conflict-recovery':'冲突恢复','offline-play':'离线游玩',
  'sleep-resume':'休眠恢复','token-revocation':'令牌撤销','upgrade':'客户端升级','network-recovery':'网络恢复',
  'saf-rom-root':'SAF ROM 目录','saf-save-tree':'SAF 存档目录','keystore-token':'Keystore 令牌',
  'retroarch-intent':'RetroArch Intent','ppsspp-intent':'PPSSPP Intent','background-recovery':'后台恢复',
  'recorded-sync-session':'已记录同步会话'
};
function acceptanceScenarioLabel(value){return tr(acceptanceScenarioNames[value]||value)}
function resetAcceptancePreview(){hardwareAcceptancePreview=null;hardwareAcceptanceCommitStatus=null;const preview=$('#acceptance-preview');if(preview){preview.hidden=true;preview.innerHTML=''}}
function acceptanceInstalledIDs(kind){const items=kind==='driver'?hardwareAcceptanceReport?.runtime?.drivers:hardwareAcceptanceReport?.runtime?.retroarch_cores;return new Set((Array.isArray(items)?items:[]).filter(item=>item?.status==='installed').map(item=>item.id))}
function reviewableAcceptanceDrivers(){const installed=acceptanceInstalledIDs('driver'),target=hardwareAcceptanceReport?.target;return emulatorDrivers.filter(item=>item.enabled&&installed.has(item.id)&&item.targets.includes(target))}
function remainingAcceptanceDrivers(){return reviewableAcceptanceDrivers().filter(item=>!hardwareAcceptanceReviewedDrivers.has(item.id))}
function renderAcceptanceReportSummary(){
  const summary=$('#acceptance-report-summary');if(!summary)return;if(!hardwareAcceptanceReport){summary.hidden=true;summary.innerHTML='';return}
  const report=hardwareAcceptanceReport,observations=Array.isArray(report.observed_on_hardware)?report.observed_on_hardware.length:0;
  const generated=Number.isNaN(Date.parse(report.generated_at))?'—':shortDate(report.generated_at);summary.hidden=false;summary.innerHTML=`<span><small>${tr('目标环境')}</small><strong>${esc(report.target||'—')}</strong></span><span><small>${tr('报告主机')}</small><strong>${esc(`${report.host_os||'—'} / ${report.host_architecture||'—'}`)}</strong></span><span><small>${tr('生成时间')}</small><strong>${esc(generated)}</strong></span><span><small>${tr('已记录场景')}</small><strong>${esc(tr(`${observations} 个场景`))}</strong></span>`;
}
function resetAcceptanceChoices(message='请先选择报告'){
  for(const id of ['acceptance-device-profile','acceptance-driver']){const select=$(`#${id}`);if(!select)continue;select.innerHTML=`<option value="">${esc(tr(message))}</option>`;select.disabled=true}
  const core=$('#acceptance-core');if(core){core.innerHTML=`<option value="">${tr('不关联核心')}</option>`;core.disabled=true}
  const coreField=$('#acceptance-core-field');if(coreField)coreField.hidden=true;
  const button=$('#preview-acceptance');if(button)button.disabled=true;
}
function refreshAcceptanceCoreChoice(preferred=''){
  const select=$('#acceptance-core'),driver=emulatorDrivers.find(item=>item.id===$('#acceptance-driver').value),installed=acceptanceInstalledIDs('core');if(!select)return;
  const cores=retroarchCores.filter(item=>item.enabled&&installed.has(item.id)&&(!driver||item.platforms.some(platform=>driver.platforms.includes(platform))));
  select.innerHTML=`<option value="">${tr('不关联核心')}</option>`+cores.map(item=>`<option value="${esc(item.id)}">${esc(item.name)}</option>`).join('');select.disabled=!hardwareAcceptanceReport||!cores.length;
  if(preferred&&cores.some(item=>item.id===preferred))select.value=preferred;else if(driver?.launch?.requires_core&&cores.length)select.value=cores[0].id;
  const requiresCore=Boolean(driver?.launch?.requires_core),field=$('#acceptance-core-field');field.hidden=!requiresCore;field.classList.toggle('is-required',requiresCore);$('#preview-acceptance').disabled=!hardwareAcceptanceReport||!$('#acceptance-device-profile').value||!driver||(requiresCore&&!select.value);
}
function refreshHardwareAcceptanceChoices(preferPending=false){
  if(!hardwareAcceptanceReport){resetAcceptanceChoices();return}
  const report=hardwareAcceptanceReport,deviceSelect=$('#acceptance-device-profile'),driverSelect=$('#acceptance-driver'),previousDevice=deviceSelect.value,previousDriver=driverSelect.value;
  const devicesForReport=deviceProfiles.filter(item=>item.enabled&&item.target===report.target),driversForReport=reviewableAcceptanceDrivers();
  deviceSelect.disabled=!devicesForReport.length;deviceSelect.innerHTML=devicesForReport.map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('')||`<option value="">${tr('没有匹配的设备环境')}</option>`;
  driverSelect.disabled=!driversForReport.length;driverSelect.innerHTML=driversForReport.map(item=>`<option value="${esc(item.id)}">${esc(item.name)}</option>`).join('')||`<option value="">${tr('没有匹配的模拟器驱动')}</option>`;
  if(devicesForReport.some(item=>item.id===previousDevice))deviceSelect.value=previousDevice;
  const pending=preferPending?driversForReport.find(item=>!hardwareAcceptanceReviewedDrivers.has(item.id)):null;
  if(pending)driverSelect.value=pending.id;else if(driversForReport.some(item=>item.id===previousDriver))driverSelect.value=previousDriver;
  refreshAcceptanceCoreChoice();
}
function hardwareAcceptanceRequest(){return {report:hardwareAcceptanceReport,device_profile_id:$('#acceptance-device-profile').value,driver_id:$('#acceptance-driver').value,core_id:$('#acceptance-core').value}}
function renderHardwareAcceptanceCompletion(){
  const container=$('#acceptance-preview'),status=hardwareAcceptanceCommitStatus;if(!container||!status)return false;
  const level=tr(supportLevelName[status.supportLevel]||status.supportLevel),followup=tr(status.hasRemaining?'此报告仍有其他已安装模拟器待审查。':'此报告中的已安装模拟器均已审查。');container.hidden=false;container.innerHTML=`<div class="acceptance-complete"><span>${uiIcon('check')}</span><div><strong>${tr('真机证据已记录')}</strong><p>${esc(tr(`已将 ${status.updatedCount} 个适配项更新为 ${level}。`))}</p><small>${followup}</small></div>${status.hasRemaining?`<button id="continue-acceptance" class="acceptance-preview-button" type="button">${tr('继续审查其他模拟器')}</button>`:''}</div>`;
  const continueButton=$('#continue-acceptance');if(continueButton)continueButton.onclick=()=>{resetAcceptancePreview();$('#acceptance-driver').focus()};return true;
}
function renderHardwareAcceptancePreview(){
  const container=$('#acceptance-preview'),preview=hardwareAcceptancePreview;if(!container||!preview){if(!renderHardwareAcceptanceCompletion()&&container)container.hidden=true;return}
  const objects=[preview.device_profile,preview.frontend,preview.emulator_driver,preview.retroarch_core].filter(Boolean),level=tr(supportLevelName[preview.support_level]||preview.support_level||'未达到真机验证要求');
  const missing=(preview.missing_for_hardware||[]).map(acceptanceScenarioLabel),syncMissing=(preview.missing_for_sync||[]).map(acceptanceScenarioLabel);
  container.hidden=false;container.innerHTML=`<header><span class="acceptance-result-mark ${preview.eligible?'eligible':'ineligible'}">${uiIcon(preview.eligible?'check':'warning')}</span><div><small>${tr('安全预览')}</small><h3>${esc(level)}</h3><p>${esc(`${preview.target} · ${preview.host} · ${shortDate(preview.generated_at)}`)}</p></div><em class="support-level ${esc(preview.support_level||'catalogued')}">${esc(level)}</em></header><div class="acceptance-preview-body"><section><small>${tr('将更新的适配项')}</small><div class="acceptance-object-list">${objects.map(item=>`<span><strong>${esc(tr(item.name))}</strong><small>${esc(tr(supportLevelName[item.current_level]||item.current_level))}</small></span>`).join('')}</div></section><section><small>${tr('报告中的真机场景')}</small><div class="acceptance-scenarios">${(preview.scenarios||[]).map(value=>`<span>${esc(acceptanceScenarioLabel(value))}</span>`).join('')}</div>${missing.length?`<p class="acceptance-warning">${tr('尚缺真机验证场景')}：${esc(missing.join(' · '))}</p>`:''}${!missing.length&&syncMissing.length?`<p class="acceptance-note">${tr('若要达到存档同步已验证，仍需')}：${esc(syncMissing.join(' · '))}</p>`:''}</section></div>${preview.eligible?`<footer><label class="acceptance-confirm"><input id="acceptance-confirm" type="checkbox"><span><strong>${tr('我已在真实设备上亲自完成以上场景')}</strong><small>${tr('确认后只保存日期、公开环境、场景和报告摘要，不保存本地路径或文件名。')}</small></span></label><button id="commit-acceptance" class="primary" type="button" disabled>${tr('确认并记录证据')}</button></footer>`:`<footer class="acceptance-ineligible"><p>${tr('报告尚未达到最低真机运行验证要求，不能写入支持证据。')}</p></footer>`}`;
  const confirm=$('#acceptance-confirm'),commit=$('#commit-acceptance');if(confirm&&commit){confirm.onchange=()=>commit.disabled=!confirm.checked;commit.onclick=commitHardwareAcceptance}
}
async function commitHardwareAcceptance(){
  const button=$('#commit-acceptance');if(!button||!hardwareAcceptancePreview?.preview_token)return;button.disabled=true;button.textContent=tr('正在记录证据…');
  const request=hardwareAcceptanceRequest();
  try{
    const result=await api('/api/hardware-acceptance/commit',{method:'POST',body:JSON.stringify({...request,preview_token:hardwareAcceptancePreview.preview_token})});
    hardwareAcceptanceReviewedDrivers.add(request.driver_id);
    await Promise.all([loadRuntimeCatalog(),loadSupportReadiness()]);renderAcceptanceReportSummary();refreshHardwareAcceptanceChoices(true);
    const updatedCount=Object.values(result.updated||{}).filter(Boolean).length,remaining=remainingAcceptanceDrivers();hardwareAcceptancePreview=null;hardwareAcceptanceCommitStatus={updatedCount,supportLevel:result.support_level,hasRemaining:Boolean(remaining.length)};renderHardwareAcceptanceCompletion();toast(tr('真机验收证据已保存'))
  }catch(error){button.disabled=false;button.textContent=tr('确认并记录证据');toast(error.message,true)}
}
$('#acceptance-file').onchange=async event=>{
  resetAcceptancePreview();hardwareAcceptanceReport=null;hardwareAcceptanceReviewedDrivers=new Set();resetAcceptanceChoices();const file=event.target.files?.[0];$('#acceptance-file-name').textContent=file?.name||tr('尚未选择报告');if(!file){renderAcceptanceReportSummary();return}
  if(file.size>512*1024){toast(tr('验收报告不能超过 512 KB'),true);event.target.value='';$('#acceptance-file-name').textContent=tr('尚未选择报告');return}
  try{const report=JSON.parse(await file.text());if(!report||report.format!=='varkiv-hardware-acceptance-v1')throw new Error(tr('这不是受支持的 Device Agent 验收报告'));hardwareAcceptanceReport=report;renderAcceptanceReportSummary();refreshHardwareAcceptanceChoices();toast(tr('报告已在浏览器本地读取，请核对适配关系'))}catch(error){event.target.value='';$('#acceptance-file-name').textContent=tr('尚未选择报告');renderAcceptanceReportSummary();toast(error.message,true)}
};
$('#acceptance-device-profile').onchange=resetAcceptancePreview;
$('#acceptance-driver').onchange=()=>{resetAcceptancePreview();refreshAcceptanceCoreChoice()};
$('#acceptance-core').onchange=()=>{resetAcceptancePreview();refreshAcceptanceCoreChoice($('#acceptance-core').value)};
$('#acceptance-review-form').onsubmit=async event=>{event.preventDefault();const button=$('#preview-acceptance');if(!hardwareAcceptanceReport)return;button.disabled=true;button.textContent=tr('正在检查报告…');try{hardwareAcceptancePreview=await api('/api/hardware-acceptance/preview',{method:'POST',body:JSON.stringify(hardwareAcceptanceRequest())});renderHardwareAcceptancePreview()}catch(error){resetAcceptancePreview();toast(error.message,true)}finally{button.textContent=tr('生成安全预览');refreshAcceptanceCoreChoice($('#acceptance-core').value)}};

function cleanupStatusLabel(status){return tr({prepared:'等待恢复',quarantined:'可恢复',restored:'已恢复'}[status]||status)}
function renderManagedCleanupRuns(){
  const list=$('#storage-recovery-list'),count=$('#storage-recovery-count');if(!list||!count)return;count.textContent=managedCleanupRuns.length;
  list.innerHTML=managedCleanupRuns.map(run=>`<div class="storage-recovery-row"><span><strong>${esc(tr(`${run.item_count} 个文件 · ${compactBytes(run.total_bytes)}`))}</strong><small>${esc(shortDate(run.created_at))} · ${esc(cleanupStatusLabel(run.status))}</small></span>${run.status==='restored'?`<em>${esc(tr('已恢复'))}</em>`:`<button type="button" data-cleanup-restore="${esc(run.id)}">${esc(tr('恢复全部文件'))}</button>`}</div>`).join('')||`<p>${tr('还没有隔离记录')}</p>`;
  list.querySelectorAll('[data-cleanup-restore]').forEach(button=>button.onclick=()=>restoreManagedCleanup(button.dataset.cleanupRestore,button));
}
async function loadManagedCleanupRuns(){try{managedCleanupRuns=await api('/api/storage-cleanup/runs');renderManagedCleanupRuns()}catch(error){toast(error.message,true)}}
function selectedManagedCleanupIDs(){return [...document.querySelectorAll('#storage-cleanup-preview input[data-cleanup-id]:checked')].map(input=>input.dataset.cleanupId)}
function updateManagedCleanupSelection(){
  const selected=selectedManagedCleanupIDs(),all=[...document.querySelectorAll('#storage-cleanup-preview input[data-cleanup-id]')],master=$('#select-all-cleanup'),confirm=$('#confirm-storage-cleanup'),commit=$('#commit-storage-cleanup'),summary=$('#storage-cleanup-selection');
  if(master){master.checked=all.length>0&&selected.length===all.length;master.indeterminate=selected.length>0&&selected.length<all.length}if(summary)summary.textContent=tr(`已选择 ${selected.length} / ${all.length} 个文件`);if(commit)commit.disabled=!selected.length||!confirm?.checked;
}
function renderManagedCleanupPreview(){
  const container=$('#storage-cleanup-preview'),preview=managedCleanupPreview;if(!container)return;if(!preview){container.hidden=true;container.innerHTML='';return}
  const items=preview.candidates||[];container.hidden=false;if(!items.length){container.innerHTML=`<div class="storage-cleanup-empty"><strong>${tr('受管存储没有未关联文件')}</strong><p>${tr('所有受管 ROM 与媒体仍有资料库关联；来源目录和存档从未进入扫描范围。')}</p></div>`;return}
  container.innerHTML=`<header><div><strong>${tr('审查待隔离文件')}</strong><small id="storage-cleanup-selection">${esc(tr(`已选择 ${items.length} / ${items.length} 个文件`))}</small></div><span>${esc(compactBytes(preview.total_bytes))} · ${items.length}</span></header><div class="storage-cleanup-list">${items.map(item=>`<label class="storage-cleanup-item"><input type="checkbox" data-cleanup-id="${esc(item.id)}" checked><em>${esc(tr(item.storage_kind==='rom'?'ROM':'媒体'))}</em><span><strong title="${esc(item.relative_path)}">${esc(item.relative_path)}</strong><small>${esc(tr('仅显示受管根内的相对路径'))}</small></span><b>${esc(compactBytes(item.size))}</b></label>`).join('')}</div><footer><label class="storage-cleanup-confirm"><input id="confirm-storage-cleanup" type="checkbox"><span><strong>${tr('我已核对这些未关联文件')}</strong><small>${tr('确认后只移入私有恢复区，不删除文件，也不触碰来源目录。')}</small></span></label><div class="storage-cleanup-actions"><button id="cancel-storage-cleanup" type="button">${tr('取消')}</button><button id="commit-storage-cleanup" class="primary" type="button" disabled>${tr('移入恢复区')}</button></div></footer>`;
  container.querySelectorAll('input[data-cleanup-id]').forEach(input=>input.onchange=updateManagedCleanupSelection);$('#confirm-storage-cleanup').onchange=updateManagedCleanupSelection;$('#cancel-storage-cleanup').onclick=()=>{managedCleanupPreview=null;renderManagedCleanupPreview()};$('#commit-storage-cleanup').onclick=commitManagedCleanup;
}
async function previewManagedCleanup(){const button=$('#preview-storage-cleanup');button.disabled=true;button.textContent=tr('正在检查受管存储…');try{managedCleanupPreview=await api('/api/storage-cleanup/preview',{method:'POST',body:'{}'});renderManagedCleanupPreview()}catch(error){managedCleanupPreview=null;renderManagedCleanupPreview();toast(error.message,true)}finally{button.disabled=false;button.textContent=tr('生成清理预览')}}
async function commitManagedCleanup(){
  const button=$('#commit-storage-cleanup'),selected=selectedManagedCleanupIDs();if(!button||!managedCleanupPreview||!selected.length)return;button.disabled=true;button.textContent=tr('正在移入恢复区…');
  try{const run=await api('/api/storage-cleanup/commit',{method:'POST',body:JSON.stringify({preview_token:managedCleanupPreview.preview_token,selected_ids:selected})});managedCleanupPreview=null;renderManagedCleanupPreview();await loadManagedCleanupRuns();toast(tr(`已安全隔离 ${run.item_count} 个文件，可随时恢复`))}catch(error){button.disabled=false;button.textContent=tr('移入恢复区');toast(error.message,true)}
}
async function restoreManagedCleanup(id,button){if(!confirm(tr('恢复会占用原路径；如果那里已有文件，整批恢复会停止且不会覆盖。继续吗？')))return;button.disabled=true;try{const run=await api(`/api/storage-cleanup/runs/${encodeURIComponent(id)}/restore`,{method:'POST',body:'{}'});await loadManagedCleanupRuns();toast(tr(`已恢复 ${run.item_count} 个文件`))}catch(error){toast(error.message,true)}finally{button.disabled=false}}
$('#preview-storage-cleanup').onclick=previewManagedCleanup;

function runtimeCollection(kind){return kind==='source'?sourceAdapters:kind==='driver'?emulatorDrivers:kind==='core'?retroarchCores:kind==='device'?deviceProfiles:frontendAdapters}
function runtimeEndpoint(kind){return kind==='source'?'source-adapters':kind==='driver'?'emulator-drivers':kind==='core'?'retroarch-cores':kind==='device'?'device-profiles':'frontend-adapters'}
function lines(value){return String(value||'').split(/\r?\n/).map(item=>item.trim()).filter(Boolean)}
function terms(value){return String(value||'').split(/[\s,]+/).map(item=>item.trim().toLowerCase()).filter(Boolean).filter((item,index,array)=>array.indexOf(item)===index)}
function assignments(value,multiple=false){const result={};for(const line of lines(value)){const separator=line.indexOf('=');if(separator<1)throw new Error(tr('每一行都必须使用“名称=值”格式'));const key=line.slice(0,separator).trim(),raw=line.slice(separator+1).trim();if(!key||!raw)throw new Error(tr('名称和值都不能为空'));result[key]=multiple?raw.split(',').map(item=>item.trim()).filter(Boolean):raw}return result}
function booleanAssignments(value){const raw=assignments(value);const result={};for(const [key,item] of Object.entries(raw)){if(item!=='true'&&item!=='false')throw new Error(tr('布尔 Extra 只能填写 true 或 false'));result[key]=item==='true'}return result}
function runtimeEditorTitle(kind,mode='new',name=''){
  const noun=kind==='source'?'来源适配器':kind==='frontend'?'前端适配器':kind==='driver'?'模拟器驱动':kind==='core'?'RetroArch 核心':'设备环境';
  if(mode==='builtin')return `${name} · ${tr(`${noun}详情`)}`;
  return tr(`${mode==='custom'?'编辑':'添加'}自定义${noun}`);
}
function setRuntimeEditorKind(kind,mode='new',name=''){
  kind=['source','frontend','driver','core','device'].includes(kind)?kind:'driver';$('#runtime-editor-kind').value=kind;
  $('#runtime-source-fields').hidden=kind!=='source';$('#runtime-frontend-fields').hidden=kind!=='frontend';$('#runtime-driver-fields').hidden=kind!=='driver';$('#runtime-core-fields').hidden=kind!=='core';$('#runtime-device-fields').hidden=kind!=='device';
  $('#runtime-editor-title').textContent=runtimeEditorTitle(kind,mode,name);
}
function openRuntimeEditor(kind='driver',id=''){
  const form=$('#runtime-editor-form'),item=runtimeCollection(kind).find(value=>value.id===id);editingRuntimeKind=kind;editingRuntimeID=id;form.reset();
  form.querySelectorAll('input,select,textarea').forEach(control=>control.disabled=false);$('#runtime-editor-kind').disabled=Boolean(id);$('#runtime-editor-readonly').hidden=true;$('#save-runtime-item').hidden=false;$('#delete-runtime-item').hidden=true;
  setRuntimeEditorKind(kind,item?(item.builtin?'builtin':'custom'):'new',item?.name||'');form.elements.default_frontend_id.innerHTML='<option value="">'+tr('不指定默认前端')+'</option>'+frontendAdapters.filter(value=>value.enabled).map(value=>`<option value="${esc(value.id)}">${esc(value.name)}</option>`).join('');
  renderRuntimeEvidence(item);if(!item){if(kind==='driver'){form.elements.arguments.value='{{rom.path}}';form.elements.save_layout.value='single-file'}$('#runtime-editor-dialog').showModal();return}
  form.elements.name.value=item.name||'';
  if(kind==='source'){
    form.elements.source_format.value=item.format||'';form.elements.source_handler.value=item.handler||'pegasus';form.elements.source_contract.value=item.contract_version||1;form.elements.source_capabilities.value=Object.entries(item.capabilities||{}).map(([key,value])=>`${key}=${value}`).join('\n');
  }else if(kind==='frontend'){
    form.elements.frontend_format.value=item.format||'';form.elements.frontend_handler.value=item.handler||'pegasus';form.elements.frontend_contract.value=item.contract_version||1;form.elements.frontend_capabilities.value=Object.entries(item.capabilities||{}).map(([key,value])=>`${key}=${value}`).join('\n');
  }else if(kind==='driver'){
    form.elements.family.value=item.family||'';form.elements.contract_version.value=item.contract_version||1;form.elements.platforms.value=(item.platforms||[]).join(', ');form.elements.targets.value=(item.targets||[]).join(', ');form.elements.executables.value=Object.entries(item.launch?.executables||{}).map(([target,values])=>`${target}=${values.join(',')}`).join('\n');form.elements.config_paths.value=Object.entries(item.config_paths||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.elements.arguments.value=(item.launch?.arguments||[]).join('\n');form.elements.requires_core.checked=Boolean(item.launch?.requires_core);form.elements.save_scope.value=item.save?.scope||'game';form.elements.save_layout.value=item.save?.layout||'';form.elements.save_refresh.value=item.save?.refresh||'process-exit';form.elements.save_portability.value=item.save?.portability||'same-driver';form.elements.save_patterns.value=(item.save?.patterns||[]).join('\n');form.elements.save_scope_by_platform.value=Object.entries(item.save?.scope_by_platform||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.elements.save_layout_by_platform.value=Object.entries(item.save?.layout_by_platform||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.elements.save_patterns_by_platform.value=Object.entries(item.save?.patterns_by_platform||{}).map(([key,value])=>`${key}=${value.join(',')}`).join('\n');const intent=item.launch?.android_intent||{};form.elements.android_package.value=intent.package||'';form.elements.android_package_candidates.value=(intent.package_candidates||[]).join('\n');form.elements.android_activity.value=intent.activity||'';form.elements.android_action.value=intent.action||'android.intent.action.VIEW';form.elements.android_data.value=intent.data||'';form.elements.android_string_extras.value=Object.entries(intent.string_extras||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.elements.android_boolean_extras.value=Object.entries(intent.boolean_extras||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.querySelectorAll('[name="android_flags"]').forEach(input=>input.checked=(intent.flags||[]).includes(input.value));
  }else if(kind==='core'){
    form.elements.library_names.value=(item.library_names||[]).join('\n');form.elements.core_platforms.value=(item.platforms||[]).join(', ');
  }else{
    form.elements.device_target.value=item.target||'';form.elements.os_family.value=item.os_family||'';form.elements.distribution.value=item.distribution||'';form.elements.architecture.value=item.architecture||'';form.elements.path_style.value=item.path_style||'posix';form.elements.max_path.value=item.max_path||255;form.elements.device_paths.value=Object.entries(item.paths||{}).map(([key,value])=>`${key}=${value}`).join('\n');form.elements.default_frontend_id.value=item.default_frontend_id||'';form.elements.case_sensitive.checked=Boolean(item.case_sensitive);form.elements.supports_hardlink.checked=Boolean(item.supports_hardlink);form.elements.supports_hooks.checked=Boolean(item.supports_hooks);
  }
  if(item.builtin){form.querySelectorAll('input,select,textarea').forEach(control=>control.disabled=true);$('#runtime-editor-readonly').hidden=false;$('#save-runtime-item').hidden=true}else{$('#delete-runtime-item').hidden=false}
  $('#runtime-editor-dialog').showModal();
}
$('#runtime-editor-kind').onchange=event=>setRuntimeEditorKind(event.target.value);
$('#new-runtime-item').onclick=()=>openRuntimeEditor('driver');
$('#runtime-editor-form').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,kind=$('#runtime-editor-kind').value,item=runtimeCollection(kind).find(value=>value.id===editingRuntimeID),button=$('#save-runtime-item'),common={name:form.elements.name.value.trim(),support_level:item?.support_level||'catalogued',evidence:item?.evidence||{scope:'user',note:'User-authored declarative adapter; device verification is recorded separately.'},enabled:item?.enabled??true};let payload;
  try{
    if(kind==='source')payload={...common,format:form.elements.source_format.value.trim(),handler:form.elements.source_handler.value,contract_version:Number(form.elements.source_contract.value||1),capabilities:booleanAssignments(form.elements.source_capabilities.value)};
    else if(kind==='frontend')payload={...common,format:form.elements.frontend_format.value.trim(),handler:form.elements.frontend_handler.value,contract_version:Number(form.elements.frontend_contract.value||1),capabilities:booleanAssignments(form.elements.frontend_capabilities.value)};
    else if(kind==='driver'){
      const androidPackage=form.elements.android_package.value.trim(),previousIntent=item?.launch?.android_intent||{},intent=androidPackage?{...previousIntent,action:form.elements.android_action.value.trim()||'android.intent.action.VIEW',package:androidPackage,package_candidates:lines(form.elements.android_package_candidates.value),activity:form.elements.android_activity.value.trim(),data:form.elements.android_data.value.trim(),string_extras:assignments(form.elements.android_string_extras.value),boolean_extras:booleanAssignments(form.elements.android_boolean_extras.value),flags:[...form.querySelectorAll('[name="android_flags"]:checked')].map(input=>input.value)}:undefined;
      payload={...common,family:form.elements.family.value.trim(),contract_version:Number(form.elements.contract_version.value||1),platforms:terms(form.elements.platforms.value),targets:terms(form.elements.targets.value),launch:{requires_core:form.elements.requires_core.checked,executables:assignments(form.elements.executables.value,true),arguments:lines(form.elements.arguments.value),...(intent?{android_intent:intent}:{})},save:{scope:form.elements.save_scope.value,layout:form.elements.save_layout.value.trim(),patterns:lines(form.elements.save_patterns.value),scope_by_platform:assignments(form.elements.save_scope_by_platform.value),layout_by_platform:assignments(form.elements.save_layout_by_platform.value),patterns_by_platform:assignments(form.elements.save_patterns_by_platform.value,true),refresh:form.elements.save_refresh.value,portability:form.elements.save_portability.value.trim()},config_paths:assignments(form.elements.config_paths.value)};
    }else if(kind==='core')payload={...common,contract_version:item?.contract_version||1,library_names:lines(form.elements.library_names.value),platforms:terms(form.elements.core_platforms.value)};
    else payload={...common,contract_version:item?.contract_version||1,target:form.elements.device_target.value.trim(),os_family:form.elements.os_family.value.trim(),distribution:form.elements.distribution.value.trim(),architecture:form.elements.architecture.value.trim(),path_style:form.elements.path_style.value,max_path:Number(form.elements.max_path.value||255),case_sensitive:form.elements.case_sensitive.checked,illegal_characters:item?.illegal_characters||'',supports_hardlink:form.elements.supports_hardlink.checked,supports_hooks:form.elements.supports_hooks.checked,default_frontend_id:form.elements.default_frontend_id.value,paths:assignments(form.elements.device_paths.value)};
    button.disabled=true;await api(`/api/${runtimeEndpoint(kind)}${editingRuntimeID?'/'+encodeURIComponent(editingRuntimeID):''}`,{method:editingRuntimeID?'PUT':'POST',body:JSON.stringify(payload)});$('#runtime-editor-dialog').close();toast(tr(editingRuntimeID?'自定义适配已更新':'自定义适配已创建'));await loadRuntimeCatalog();
  }catch(error){toast(error.message,true)}finally{button.disabled=false}
};
$('#delete-runtime-item').onclick=async()=>{
  const item=runtimeCollection(editingRuntimeKind).find(value=>value.id===editingRuntimeID);if(!item||item.builtin||!confirm(tr('只删除这条自定义适配元数据；不会删除 ROM、模拟器、核心文件、整合包或存档。')))return;const button=$('#delete-runtime-item');button.disabled=true;try{await api(`/api/${runtimeEndpoint(editingRuntimeKind)}/${encodeURIComponent(editingRuntimeID)}`,{method:'DELETE'});$('#runtime-editor-dialog').close();toast(tr('自定义适配已删除'));await loadRuntimeCatalog()}catch(error){toast(error.message,true)}finally{button.disabled=false}
};
function renderPairingProfiles(){const select=$('#pair-device-profile');if(!select)return;const current=select.value;select.innerHTML=deviceProfiles.filter(item=>item.enabled).map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('');if(deviceProfiles.some(item=>item.id===current))select.value=current}
async function loadRuntimeCatalog(){
  try{[sourceAdapters,frontendAdapters,deviceProfiles,emulatorDrivers,retroarchCores,coreMappings,launchBindings,runtimeImportHints,saveStreams,saveBindings,saveCompatibilityGroups]=await Promise.all([api('/api/source-adapters'),api('/api/frontend-adapters'),api('/api/device-profiles'),api('/api/emulator-drivers'),api('/api/retroarch-cores'),api('/api/core-mappings'),api('/api/launch-bindings'),api('/api/runtime-import-hints'),api('/api/save-streams'),api('/api/save-bindings'),api('/api/save-compatibility-groups')]);renderRuntimeCatalog();refreshHardwareAcceptanceChoices();renderSourceAdapterChoice();renderLibrarySources();renderPairingProfiles();renderSyncStatus()}catch(e){toast(`运行配置加载失败：${e.message}`,true)}
}
function selectedLaunchBinding(){return launchBindings.find(binding=>binding.edition_id===editingEditionID&&binding.device_profile_id===$('#launch-device').value)}
function refreshLaunchDrivers(preferred=''){
  const found=findEdition(editingEditionID),device=deviceProfiles.find(item=>item.id===$('#launch-device').value);if(!found||!device)return;
  const options=emulatorDrivers.filter(item=>item.enabled&&item.platforms.includes(found.game.platform)&&item.targets.includes(device.target));$('#launch-driver').innerHTML=options.map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('')||`<option value="">${tr('没有匹配的模拟器驱动')}</option>`;if(preferred&&options.some(item=>item.id===preferred))$('#launch-driver').value=preferred;
  const cores=retroarchCores.filter(item=>item.enabled&&item.platforms.includes(found.game.platform));$('#launch-core').innerHTML=`<option value="">${tr('使用分层默认映射')}</option>`+cores.map(item=>`<option value="${esc(item.id)}">${esc(item.name)} · ${esc(item.library_names[0]||item.id)}</option>`).join('');
}
function syncLaunchEditor(){
  selectedRuntimeHintID='';$('#apply-runtime-hint').hidden=true;$('#preview-runtime-hint-batch').hidden=true;clearRuntimeHintBatchPreview();const binding=selectedLaunchBinding();refreshLaunchDrivers(binding?.driver_id||'');if(binding){$('#launch-driver').value=binding.driver_id;$('#launch-core').value=binding.core_id||'';$('#launch-arguments').value=(binding.arguments||[]).join('\n');$('#launch-binding-state').textContent=tr('已配置');$('#delete-launch-binding').hidden=false}else{$('#launch-arguments').value='';$('#launch-core').value='';$('#launch-binding-state').textContent=tr('未配置');$('#delete-launch-binding').hidden=true}$('#launch-preview').hidden=true;
}
function renderEditionLaunchBindings(edition){
  const existing=launchBindings.find(item=>item.edition_id===edition.id&&item.device_profile_id),selected=existing?.device_profile_id||deviceProfiles.find(item=>item.enabled)?.id||'';$('#launch-device').innerHTML=deviceProfiles.filter(item=>item.enabled).map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('');$('#launch-device').value=selected;syncLaunchEditor();
}
async function persistLaunchBinding(showToast=true){
  const deviceID=$('#launch-device').value,driverID=$('#launch-driver').value;if(!deviceID||!driverID)throw new Error(tr('请选择目标设备和模拟器驱动'));const existing=selectedLaunchBinding(),device=deviceProfiles.find(item=>item.id===deviceID),payload={edition_id:editingEditionID,device_profile_id:deviceID,driver_id:driverID,frontend_adapter_id:device?.default_frontend_id||'',core_id:$('#launch-core').value,arguments:$('#launch-arguments').value.split(/\r?\n/).map(item=>item.trim()).filter(Boolean),enabled:true};const saved=await api(existing?`/api/launch-bindings/${existing.id}`:'/api/launch-bindings',{method:existing?'PUT':'POST',body:JSON.stringify(payload)});launchBindings=await api('/api/launch-bindings');syncLaunchEditor();if(showToast)toast(tr('启动绑定已保存'));return saved;
}
function renderRuntimeImportHints(edition){
  const box=$('#runtime-import-hints'),hints=runtimeImportHints.filter(item=>item.edition_id===edition.id&&item.status==='pending');selectedRuntimeHintID='';$('#apply-runtime-hint').hidden=true;$('#preview-runtime-hint-batch').hidden=true;clearRuntimeHintBatchPreview();if(!hints.length){box.hidden=true;box.innerHTML='';return}box.hidden=false;
  box.innerHTML=`<header><span><strong>${tr('发现启动配置建议')}</strong><small>${tr('导入内容不会自动执行；请先核对设备、模拟器、核心和参数。')}</small></span><b>${hints.length}</b></header><div>${hints.map(hint=>{const structured=hint.trust==='structured',driver=emulatorDrivers.find(item=>item.id===hint.driver_id)?.name||hint.driver_id||'未识别模拟器',core=retroarchCores.find(item=>item.id===hint.core_id)?.name||hint.core_id||'未指定核心',source=hint.source_ref||hint.source_format;return `<article class="runtime-import-hint ${structured?'structured':'untrusted'}"><div class="runtime-hint-copy"><span><em>${structured?tr('结构化建议'):tr('未受信任命令')}</em><small>${esc(source)}</small></span><strong><span>${esc(tr(driver))}</span><span aria-hidden="true">·</span><span>${esc(tr(core))}</span></strong>${hint.raw_command?`<code>${esc(hint.raw_command)}</code><p>${tr('原始命令仅作为参考显示，Varkiv 永远不会执行它。')}</p>`:`<p>${tr('来自 Varkiv 整合包的声明式绑定，可在核对后恢复。')}</p>`}</div><footer><button type="button" data-review-runtime-hint="${esc(hint.id)}">${tr('填入审核')}</button><button type="button" class="quiet-danger" data-dismiss-runtime-hint="${esc(hint.id)}">${tr('忽略建议')}</button></footer></article>`}).join('')}</div>`;
  box.querySelectorAll('[data-review-runtime-hint]').forEach(button=>button.onclick=()=>reviewRuntimeImportHint(button.dataset.reviewRuntimeHint));box.querySelectorAll('[data-dismiss-runtime-hint]').forEach(button=>button.onclick=()=>dismissRuntimeImportHint(button.dataset.dismissRuntimeHint));
}
function reviewRuntimeImportHint(id){
  const hint=runtimeImportHints.find(item=>item.id===id),found=findEdition(editingEditionID);if(!hint||!found)return;const suggestedDevice=deviceProfiles.find(item=>item.id===hint.device_profile_id&&item.enabled);if(suggestedDevice)$('#launch-device').value=suggestedDevice.id;refreshLaunchDrivers(hint.driver_id);if([...$('#launch-driver').options].some(option=>option.value===hint.driver_id))$('#launch-driver').value=hint.driver_id;if([...$('#launch-core').options].some(option=>option.value===hint.core_id))$('#launch-core').value=hint.core_id;$('#launch-arguments').value=(hint.arguments||[]).join('\n');selectedRuntimeHintID=id;$('#apply-runtime-hint').hidden=false;clearRuntimeHintBatchPreview();updateRuntimeHintBatchButton();$('#launch-preview').hidden=true;toast(tr('建议已填入，请核对后再应用'));
}
function clearRuntimeHintBatchPreview(){runtimeHintBatchPreview=null;const panel=$('#runtime-hint-batch-preview');if(panel){panel.hidden=true;panel.innerHTML=''}}
function matchingRuntimeHintBatch(){
  const selected=runtimeImportHints.find(item=>item.id===selectedRuntimeHintID),selectedEdition=selected&&findEdition(selected.edition_id);if(!selected||!selectedEdition)return[];
  return runtimeImportHints.filter(item=>{const found=findEdition(item.edition_id);return item.status==='pending'&&found?.game.platform===selectedEdition.game.platform&&item.source_kind===selected.source_kind&&item.source_format===selected.source_format&&item.trust===selected.trust&&(item.driver_id||'')===(selected.driver_id||'')&&(item.core_id||'')===(selected.core_id||'')});
}
function updateRuntimeHintBatchButton(){const button=$('#preview-runtime-hint-batch'),count=matchingRuntimeHintBatch().length;button.hidden=count<2;button.textContent=count<2?tr('批量审核同类建议'):`${tr('批量审核同类建议')} · ${count}`}
function runtimeHintBatchRequest(){
  const hint=runtimeImportHints.find(item=>item.id===selectedRuntimeHintID),device=deviceProfiles.find(item=>item.id===$('#launch-device').value),matches=matchingRuntimeHintBatch();if(!hint||!device||!$('#launch-driver').value||matches.length<2)return null;
  const frontendID=frontendAdapters.some(item=>item.id===hint.frontend_adapter_id&&item.enabled)?hint.frontend_adapter_id:device.default_frontend_id||'';
  return{hint_ids:matches.map(item=>item.id),device_profile_id:device.id,driver_id:$('#launch-driver').value,frontend_adapter_id:frontendID,core_id:$('#launch-core').value,arguments:$('#launch-arguments').value.split(/\r?\n/).map(item=>item.trim()).filter(Boolean)};
}
function renderRuntimeHintBatchPreview(){
  const panel=$('#runtime-hint-batch-preview'),preview=runtimeHintBatchPreview;if(!preview){clearRuntimeHintBatchPreview();return}const device=deviceProfiles.find(item=>item.id===preview.device_profile_id),driver=emulatorDrivers.find(item=>item.id===preview.driver_id),core=retroarchCores.find(item=>item.id===preview.core_id);
  panel.innerHTML=`<header><div><small>${esc(tr('批量启动审核'))}</small><strong>${esc(tr('同一配置应用到同平台版本'))}</strong></div><b>${preview.count}</b></header><dl><div><dt>${esc(tr('平台'))}</dt><dd>${esc(syncPlatformName(preview.platform_id))}</dd></div><div><dt>${esc(tr('目标设备'))}</dt><dd>${esc(tr(device?.name||preview.device_profile_id))}</dd></div><div><dt>${esc(tr('模拟器驱动'))}</dt><dd>${esc(tr(driver?.name||preview.driver_id))}</dd></div><div><dt>${esc(tr('RetroArch 核心'))}</dt><dd>${esc(core?.name||tr('不需要核心'))}</dd></div></dl><p>${esc(tr('提交时会重新核对全部建议、版本与运行配置；任一项变化或冲突都会整批取消。原始命令不会执行或复制。'))}</p><footer><button type="button" data-cancel-runtime-batch>${esc(tr('返回修改'))}</button><button type="button" class="primary" data-commit-runtime-batch>${esc(tr('确认批量应用'))} · ${preview.count}</button></footer>`;panel.hidden=false;panel.querySelector('[data-cancel-runtime-batch]').onclick=clearRuntimeHintBatchPreview;panel.querySelector('[data-commit-runtime-batch]').onclick=commitRuntimeHintBatch;
}
async function previewRuntimeHintBatch(){
  const request=runtimeHintBatchRequest(),button=$('#preview-runtime-hint-batch');if(!request){toast(tr('请选择目标设备和模拟器驱动'),true);return}button.disabled=true;try{const preview=await api('/api/runtime-import-hints/batch/preview',{method:'POST',body:JSON.stringify(request)});runtimeHintBatchPreview={...preview,request};renderRuntimeHintBatchPreview()}catch(error){clearRuntimeHintBatchPreview();toast(error.message,true)}finally{button.disabled=false}
}
async function commitRuntimeHintBatch(event){
  const button=event.currentTarget,preview=runtimeHintBatchPreview;if(!preview)return;button.disabled=true;try{const result=await api('/api/runtime-import-hints/batch/commit',{method:'POST',body:JSON.stringify({...preview.request,preview_token:preview.preview_token})});[launchBindings,runtimeImportHints]=await Promise.all([api('/api/launch-bindings'),api('/api/runtime-import-hints')]);const found=findEdition(editingEditionID);if(found){renderEditionLaunchBindings(found.edition);renderRuntimeImportHints(found.edition)}toast(`${tr('批量启动绑定已应用')} · ${result.applied}`)}catch(error){clearRuntimeHintBatchPreview();toast(error.message,true)}finally{button.disabled=false}
}
async function dismissRuntimeImportHint(id){
  if(!confirm(tr('忽略这条导入建议？只改变建议状态，不会删除 ROM、媒体或前端文件。')))return;try{await api(`/api/runtime-import-hints/${encodeURIComponent(id)}/dismiss`,{method:'POST'});runtimeImportHints=await api('/api/runtime-import-hints');const found=findEdition(editingEditionID);if(found)renderRuntimeImportHints(found.edition);toast(tr('导入建议已忽略'))}catch(e){toast(e.message,true)}
}
function selectedSaveBinding(){return saveBindings.find(binding=>binding.edition_id===editingEditionID&&binding.device_profile_id===$('#save-device').value)}
function refreshSaveDrivers(preferred=''){
  const found=findEdition(editingEditionID),device=deviceProfiles.find(item=>item.id===$('#save-device').value);if(!found||!device)return;
  const options=emulatorDrivers.filter(item=>item.enabled&&item.platforms.includes(found.game.platform)&&item.targets.includes(device.target));$('#save-driver').innerHTML=options.map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('')||`<option value="">${tr('没有匹配的模拟器驱动')}</option>`;if(preferred&&options.some(item=>item.id===preferred))$('#save-driver').value=preferred;
}
function refreshSaveCores(preferred=''){
  const found=findEdition(editingEditionID),driver=emulatorDrivers.find(item=>item.id===$('#save-driver').value),required=Boolean(driver?.launch?.requires_core),field=$('#save-core-field'),select=$('#save-core');field.hidden=!required;select.disabled=!required;
  if(!required){select.innerHTML='';select.value='';return}
  const options=retroarchCores.filter(item=>item.enabled&&found&&item.platforms.includes(found.game.platform));select.innerHTML=`<option value="">${tr('请选择 RetroArch 核心')}</option>${options.map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('')}`;
  const deviceID=$('#save-device').value,scopes=[['edition',editingEditionID],['device_profile',deviceID],['platform',''],['global','']],mapping=scopes.map(([scope_type,scope_key])=>coreMappings.filter(item=>item.platform_id===found?.game.platform&&item.scope_type===scope_type&&(item.scope_key||'')===scope_key).sort((left,right)=>(right.priority||0)-(left.priority||0)||left.id.localeCompare(right.id))[0]).find(Boolean),selected=preferred||mapping?.core_id||'';if(options.some(item=>item.id===selected))select.value=selected;
}
function normalizedRuntimeArchitecture(value){const key=String(value||'').trim().toLowerCase();return key==='aarch64'?'arm64':key==='x86_64'?'amd64':key}
function runtimeOSMatches(memberOS,profileOS){const member=String(memberOS||'').toLowerCase(),profile=String(profileOS||'').toLowerCase();return member===profile||(member==='linux'&&profile==='handheld-linux')||(member==='handheld-linux'&&profile==='linux')}
function compatibleSavePlan(){
  const found=findEdition(editingEditionID),profile=deviceProfiles.find(item=>item.id===$('#save-device').value),driverID=$('#save-driver').value,coreID=$('#save-core').value;if(!found||!profile||!driverID||!coreID)return null;
  for(const group of saveCompatibilityGroups.filter(item=>item.enabled)){
    const targetMember=(group.members||[]).find(member=>member.runtime_kind==='device'&&member.driver_id===driverID&&member.core_id===coreID&&runtimeOSMatches(member.os_family,profile.os_family)&&(!member.architecture||normalizedRuntimeArchitecture(member.architecture)===normalizedRuntimeArchitecture(profile.architecture)));
    const sourceMember=(group.members||[]).find(member=>member.runtime_kind==='server'&&!member.core_id);if(!targetMember||!sourceMember)continue;
    const stream=saveStreams.find(item=>item.compatibility_group_id===group.id&&item.driver_id===sourceMember.driver_id&&(item.editions||[]).some(link=>link.edition_id===found.edition.id));return {group,targetMember,sourceMember,stream};
  }
  return null;
}
function driverSaveContract(driver,platform){
  const save=driver?.save||{},scope=save.scope_by_platform?.[platform]||save.scope||'game',layout=save.layout_by_platform?.[platform]||save.layout||'',specific=save.patterns_by_platform?.[platform],patterns=[...(specific||save.patterns||[])];
  if(driver?.family==='retroarch')for(let index=0;index<patterns.length;index++)if(!patterns[index].includes('{{device.')&&!patterns[index].includes('{{driver.'))patterns[index]=`{{device.save_dir}}/${patterns[index]}`;
  return {scope,layout,patterns};
}
function savePathIdentityError(edition,paths){
  const uses=name=>paths.some(path=>path.includes(`{{${name}}}`)),titleID=String(edition?.title_id||'').replace(/[\s-]/g,'');
  if(uses('edition.serial')&&!String(edition?.serial||'').trim())return '请先为这个版本填写序列号';
  if(uses('edition.product_code')&&!String(edition?.product_code||'').trim())return '请先为这个版本填写产品代码';
  if((uses('edition.title_id_high')||uses('edition.title_id_low'))&&!/^[0-9a-f]{16}$/i.test(titleID))return '请先为这个版本填写 16 位十六进制标题标识';
  if(uses('edition.title_id')&&!String(edition?.title_id||'').trim())return '请先为这个版本填写标题标识';
  return '';
}
function applySaveDriverDefaults(){
  if(selectedSaveBinding())return;const found=findEdition(editingEditionID),driver=emulatorDrivers.find(item=>item.id===$('#save-driver').value);if(!found||!driver)return;const contract=driverSaveContract(driver,found.game.platform),suggested=contract.patterns.slice(0,1),ownerType=contract.scope==='game'?'edition':contract.scope;$('#save-owner-type').value=ownerType;$('#save-local-paths').value=suggested.join('\n');if(ownerType==='container'&&!$('#save-owner-key').value)$('#save-owner-key').value=`${driver.id}:default`;updateSaveOwnerField();const bridge=compatibleSavePlan(),needsRoot=suggested.some(path=>path.includes('{{driver.user_dir}}')),identityError=savePathIdentityError(found.edition,suggested),summary=$('#save-binding-summary');summary.classList.toggle('is-error',Boolean(identityError));summary.textContent=tr(identityError||(bridge?'已找到经过字节往返证明的共享格式。绑定会先保存，只有 Agent 核验模拟器与核心身份后才参与同步。':suggested.length?(needsRoot?'此驱动需要客户端显式授权模拟器用户目录；保存前仍可逐行核对路径。':'已填入驱动建议路径；保存前请核对设备上的真实目录。'):'此驱动没有可靠的内置存档路径；请填写设备上已经核对的精确文件或目录。'));summary.hidden=false;
}
function updateSaveOwnerField(){const container=$('#save-owner-type').value==='container';$('#save-owner-key-field').hidden=!container;if(!container)$('#save-owner-key').value=''}
function syncSaveEditor(){
  const found=findEdition(editingEditionID),binding=selectedSaveBinding(),stream=binding?saveStreams.find(item=>item.id===binding.stream_id):null,launch=launchBindings.find(item=>item.edition_id===editingEditionID&&item.device_profile_id===$('#save-device').value);refreshSaveDrivers(binding?.driver_id||launch?.driver_id||'');
  refreshSaveCores(binding?.core_id||launch?.core_id||'');
  if(binding&&stream){const shared=binding.driver_id!==stream.driver_id,verified=shared&&devices.some(device=>device.device_profile_id===binding.device_profile_id&&device.status!=='revoked'&&device.capabilities?.verified_save_bridge);$('#save-driver').value=binding.driver_id;$('#save-driver').disabled=true;$('#save-core').value=binding.core_id||'';$('#save-core').disabled=true;$('#save-owner-type').value=stream.owner_type;$('#save-owner-type').disabled=true;$('#save-owner-key').value=stream.owner_type==='container'?stream.owner_key:'';$('#save-local-paths').value=(binding.local_paths||[]).join('\n');$('#save-binding-state').textContent=tr(shared?(verified?'已验证共享':'待设备验真'):'已配置');$('#delete-save-binding').hidden=false;$('#save-binding-summary').classList.remove('is-error');$('#save-binding-summary').textContent=tr(shared?'模拟器或核心身份未匹配前，不会读取、下载或覆盖设备存档。':'这个绑定会自动参与已配对设备的下一次同步；移除绑定不会删除存档流、历史版本或设备文件。');$('#save-binding-summary').hidden=false}else{$('#save-driver').disabled=false;refreshSaveCores(launch?.core_id||'');$('#save-owner-type').disabled=false;$('#save-owner-key').value='';$('#save-local-paths').value='';$('#save-binding-state').textContent=tr('未配置');$('#delete-save-binding').hidden=true;applySaveDriverDefaults()}updateSaveOwnerField();
}
function renderEditionSaveBindings(edition){
  const existing=saveBindings.find(item=>item.edition_id===edition.id&&item.device_profile_id),launch=launchBindings.find(item=>item.edition_id===edition.id&&item.device_profile_id),selected=existing?.device_profile_id||launch?.device_profile_id||deviceProfiles.find(item=>item.enabled)?.id||'';$('#save-device').innerHTML=deviceProfiles.filter(item=>item.enabled).map(item=>`<option value="${esc(item.id)}">${esc(tr(item.name))}</option>`).join('');$('#save-device').value=selected;syncSaveEditor();
}
async function persistSaveBinding(){
  const found=findEdition(editingEditionID),deviceID=$('#save-device').value,driverID=$('#save-driver').value,driver=emulatorDrivers.find(item=>item.id===driverID),coreID=$('#save-core').value,paths=$('#save-local-paths').value.split(/\r?\n/).map(item=>item.trim()).filter(Boolean);if(!found||!deviceID||!driverID)throw new Error(tr('请选择目标设备和模拟器驱动'));if(driver?.launch?.requires_core&&!coreID)throw new Error(tr('请选择 RetroArch 核心'));if(!paths.length)throw new Error(tr('请至少填写一个存档路径模板'));const identityError=savePathIdentityError(found.edition,paths);if(identityError)throw new Error(tr(identityError));
  const existing=selectedSaveBinding();if(existing){await api(`/api/save-bindings/${existing.id}`,{method:'PUT',body:JSON.stringify({stream_id:existing.stream_id,edition_id:editingEditionID,device_profile_id:deviceID,driver_id:existing.driver_id,core_id:existing.core_id||coreID,local_paths:paths,discovery:{mode:'declared'},enabled:true})})}else{const ownerType=$('#save-owner-type').value,ownerKey=ownerType==='edition'?editingEditionID:ownerType==='platform'?found.game.platform:$('#save-owner-key').value.trim();if(!ownerKey)throw new Error(tr('请填写共享容器标识'));const bridge=ownerType==='edition'?compatibleSavePlan():null,binding={edition_id:editingEditionID,device_profile_id:deviceID,driver_id:driverID,core_id:coreID,local_paths:paths,discovery:{mode:'declared'},enabled:true};if(bridge?.stream){await api('/api/save-bindings',{method:'POST',body:JSON.stringify({...binding,stream_id:bridge.stream.id})})}else{const streamDriverID=bridge?.sourceMember.driver_id||driverID;await api('/api/save-bindings/setup',{method:'POST',body:JSON.stringify({stream:{owner_type:ownerType,owner_key:ownerKey,driver_id:streamDriverID,portability:bridge?'core-dependent':driverID.includes('retroarch')?'core-dependent':'driver-dependent',compatibility_group_id:bridge?.group.id||'',edition_ids:[editingEditionID],compatibility:bridge?'verified':'native'},binding})})}}
  [saveStreams,saveBindings]=await Promise.all([api('/api/save-streams'),api('/api/save-bindings')]);syncSaveEditor();toast(tr('自动存档绑定已保存'));
}
async function hydrateMediaImages(){for(const image of document.querySelectorAll('[data-media-image]')){try{const blob=await mediaImageBlob(image.dataset.mediaImage,128,image.dataset.mediaImageMime);if(!blob)continue;const url=URL.createObjectURL(blob);mediaObjectURLs.push(url);image.src=url}catch{}}}

gameForm.onsubmit=async e=>{
  e.preventDefault();const f=new FormData(e.target),titles={};['zh-CN','zh-TW','ja','en'].forEach(k=>{if(f.get(k))titles[k]=f.get(k)});
  try{await api(editingGameID?`/api/games/${editingGameID}`:'/api/games',{method:editingGameID?'PUT':'POST',body:JSON.stringify({default_title:f.get('default_title'),platform:f.get('platform'),titles})});gameDialog.close();toast(editingGameID?'游戏已更新':'游戏已创建');await load()}catch(err){toast(err.message,true)}
};
editionForm.onsubmit=async e=>{
  e.preventDefault();const f=new FormData(e.target),found=editingEditionID?findEdition(editingEditionID):null;
  const titles={};['zh-CN','zh-TW','ja','en'].forEach(l=>{if(f.get(`e-${l}`))titles[l]=f.get(`e-${l}`)});
  const payload={game_id:f.get('game_id'),default_title:f.get('default_title'),edition_type:f.get('edition_type'),version:f.get('version'),languages:f.getAll('languages'),author:f.get('author'),titles};
  if(!editingEditionID){payload.artifact_path=f.get('artifact_path');payload.artifact_role='rom'}
  try{await api(editingEditionID?`/api/editions/${editingEditionID}`:`/api/editions?locale=${encodeURIComponent(locale.value)}`,{method:editingEditionID?'PUT':'POST',body:JSON.stringify(payload)});editionDialog.close();toast(editingEditionID?'版本已更新':'独立版本已添加');await load()}catch(err){toast(err.message,true)}
};

function renderGameMergePreview(plan){
  $('#merge-preview-source').textContent=plan.source_title;$('#merge-preview-target').textContent=plan.target_title;
  $('#merge-preview-editions').textContent=`${plan.target_editions} + ${plan.source_editions} → ${plan.result_editions}`;
  $('#merge-preview-files').textContent=String(plan.source_artifacts);$('#merge-preview-media').textContent=String(plan.source_game_media+plan.source_edition_media);$('#merge-preview-names').textContent=String(plan.added_localized_titles);
}
$('#merge-game').onclick=async()=>{const source=$('#merge-source').value,target=findGame(editingGameID),button=$('#merge-game');if(!source||!target)return;button.disabled=true;try{gameMergePreview=await api(`/api/games/${target.id}/merge/preview?locale=${encodeURIComponent(locale.value)}`,{method:'POST',body:JSON.stringify({source_game_id:source})});renderGameMergePreview(gameMergePreview);gameDialog.close();mergeDialog.showModal()}catch(e){gameMergePreview=null;toast(e.message,true)}finally{button.disabled=false}};
$('#commit-game-merge').onclick=async()=>{const plan=gameMergePreview,button=$('#commit-game-merge');if(!plan)return;button.disabled=true;try{await api(`/api/games/${plan.target_game_id}/merge`,{method:'POST',body:JSON.stringify({source_game_id:plan.source_game_id,preview_token:plan.preview_token,snapshot_fingerprint:plan.snapshot_fingerprint})});gameMergePreview=null;mergeDialog.close();toast(tr('游戏已合并；版本身份与存档空间保持不变'));await load()}catch(e){gameMergePreview=null;mergeDialog.close();toast(e.message,true);await load()}finally{button.disabled=false}};
mergeDialog.addEventListener('close',()=>{gameMergePreview=null});
$('#delete-game').onclick=async()=>{const w=findGame(editingGameID);if(!w||!confirm(`删除“${w.display_title}”及其版本资料？ROM 文件不会删除。`))return;try{await api(`/api/games/${w.id}`,{method:'DELETE'});gameDialog.close();toast('资料条目已删除');await load()}catch(e){toast(e.message,true)}};
$('#move-edition').onclick=async()=>{const target=$('#move-target').value;if(!target)return;try{await api(`/api/editions/${editingEditionID}/move`,{method:'POST',body:JSON.stringify({target_game_id:target})});editionDialog.close();toast(tr('版本已移动，ID 与存档空间保持不变'));await load()}catch(e){toast(e.message,true)}};
$('#primary-edition').onclick=async()=>{const found=findEdition(editingEditionID);if(!found)return;try{await api(`/api/games/${found.game.id}/primary`,{method:'PUT',body:JSON.stringify({edition_id:editingEditionID})});editionDialog.close();toast('主版本已更新');await load()}catch(e){toast(e.message,true)}};
$('#delete-edition').onclick=async()=>{const found=findEdition(editingEditionID);if(!found||!confirm(`删除版本“${found.edition.display_title}”的资料？ROM 和存档文件不会删除。`))return;try{await api(`/api/editions/${editingEditionID}`,{method:'DELETE'});editionDialog.close();toast('版本资料已删除');await load()}catch(e){toast(e.message,true)}};
$('#add-artifact').onclick=async()=>{const path=$('#artifact-path').value.trim();if(!path){toast('请输入文件或目录路径',true);return}try{await api('/api/artifacts',{method:'POST',body:JSON.stringify({edition_id:editingEditionID,path,role:$('#artifact-role').value,disc_index:Number($('#artifact-disc').value)||0})});$('#artifact-path').value='';$('#artifact-disc').value='';toast('文件已关联');await load();const found=findEdition(editingEditionID);if(found)renderEditionFiles(found.edition)}catch(e){toast(e.message,true)}};
async function updateArtifact(id,button){const found=findEdition(editingEditionID),artifact=found?.edition.artifacts.find(a=>a.id===id),panel=button.closest('.artifact-edit-panel');if(!artifact||!panel)return;const disc=Number(panel.querySelector('.artifact-edit-disc').value);if(!Number.isInteger(disc)||disc<0||disc>64){toast(tr('碟号必须是 0 到 64 之间的整数'),true);return}button.disabled=true;try{await api(`/api/artifacts/${id}`,{method:'PUT',body:JSON.stringify({path:artifact.path,role:panel.querySelector('.artifact-edit-role').value,disc_index:disc})});toast(tr('文件语义已更新；实际文件保持不变'));await load();const refreshed=findEdition(editingEditionID);if(refreshed)renderEditionFiles(refreshed.edition)}catch(e){toast(e.message,true)}finally{button.disabled=false}}
async function removeArtifact(id){const found=findEdition(editingEditionID),artifact=found?.edition.artifacts.find(a=>a.id===id);if(!artifact||!confirm(`移除“${artifact.path}”的资料关联？实际文件不会删除。`))return;try{await api(`/api/artifacts/${id}`,{method:'DELETE'});toast('文件关联已移除');await load();const refreshed=findEdition(editingEditionID);if(refreshed)renderEditionFiles(refreshed.edition)}catch(e){toast(e.message,true)}}
$('#upload-media').onclick=async()=>{const input=$('#media-file'),file=input.files?.[0];if(!file){toast(tr('请先选择媒体文件'),true);return}const body=new FormData();body.append('file',file);body.append('edition_id',editingEditionID);body.append('kind',$('#media-kind').value);const button=$('#upload-media');button.disabled=true;button.textContent=tr('上传中…');try{await api('/api/media/upload',{method:'POST',body});resetMediaPicker('media-file','media-file-name');toast(tr('媒体已保存并去重'));await load();const found=findEdition(editingEditionID);if(found)renderEditionMedia(found.edition)}catch(e){toast(e.message,true)}finally{button.disabled=false;button.textContent=tr('上传媒体')}};
async function updateMediaMetadata(id,button,scope){const owner=scope==='game'?findGame(editingGameID):findEdition(editingEditionID)?.edition,item=owner?.media?.find(value=>value.id===id),panel=button.closest('.media-edit-panel');if(!item||!panel)return;const order=Number(panel.querySelector('.media-edit-order').value);if(!Number.isInteger(order)||order<0){toast(tr('排序必须是零或正整数'),true);return}button.disabled=true;try{const updated=await api(`/api/media/${id}`,{method:'PUT',body:JSON.stringify({kind:panel.querySelector('.media-edit-kind').value,locale:panel.querySelector('.media-edit-locale').value,sort_order:order})});if(updated.path!==item.path||updated.sha256!==item.sha256||updated.game_id!==item.game_id||updated.edition_id!==item.edition_id)throw new Error(tr('服务返回的媒体身份发生变化；界面已停止更新。'));toast(tr('媒体信息已更新；实际文件保持不变'));await load();if(scope==='game'){const refreshed=findGame(editingGameID);if(refreshed)renderGameMedia(refreshed)}else{const refreshed=findEdition(editingEditionID);if(refreshed)renderEditionMedia(refreshed.edition)}}catch(e){toast(e.message,true)}finally{button.disabled=false}}
async function recheckMediaContent(button){const scope=button.dataset.mediaScope,ownerID=scope==='game'?editingGameID:scope==='edition'?editingEditionID:'',query=ownerID?`?${scope}_id=${encodeURIComponent(ownerID)}`:'';button.disabled=true;try{const result=await api(`/api/media/recheck${query}`,{method:'POST',body:'{}'});toast(tr(`已检查 ${result.checked} 项媒体：可用 ${result.available}，缺失 ${result.missing}，变化 ${result.changed}，不安全 ${result.unsafe}`));await load();if(scope==='game'&&editingGameID){const game=findGame(editingGameID);if(game)renderGameMedia(game)}if(scope==='edition'&&editingEditionID){const found=findEdition(editingEditionID);if(found)renderEditionMedia(found.edition)}}catch(e){toast(e.message,true)}finally{button.disabled=false}}
async function removeMedia(id){const found=findEdition(editingEditionID),item=found?.edition.media?.find(x=>x.id===id);if(!item||!confirm(`移除“${item.original_name}”的媒体关系？内容 blob 将等待安全清理。`))return;try{await api(`/api/media/${id}`,{method:'DELETE'});toast('媒体关系已移除');await load();const refreshed=findEdition(editingEditionID);if(refreshed)renderEditionMedia(refreshed.edition)}catch(e){toast(e.message,true)}}
$('#upload-game-media').onclick=async()=>{const input=$('#game-media-file'),file=input.files?.[0];if(!file){toast(tr('请先选择媒体文件'),true);return}const body=new FormData();body.append('file',file);body.append('game_id',editingGameID);body.append('kind',$('#game-media-kind').value);const button=$('#upload-game-media');button.disabled=true;button.textContent=tr('上传中…');try{await api('/api/media/upload',{method:'POST',body});resetMediaPicker('game-media-file','game-media-file-name');toast(tr('游戏媒体已保存并去重'));await load();const game=findGame(editingGameID);if(game)renderGameMedia(game)}catch(e){toast(e.message,true)}finally{button.disabled=false;button.textContent=tr('上传游戏媒体')}};
async function removeGameMedia(id){const game=findGame(editingGameID),item=game?.media?.find(value=>value.id===id);if(!item||!confirm(tr(`移除“${item.original_name}”的共享媒体关系？内容文件会保留以便安全清理。`)))return;try{await api(`/api/media/${id}`,{method:'DELETE'});toast(tr('共享媒体关系已移除'));await load();const refreshed=findGame(editingGameID);if(refreshed)renderGameMedia(refreshed)}catch(e){toast(e.message,true)}}
$('#launch-device').onchange=syncLaunchEditor;
$('#launch-driver').onchange=clearRuntimeHintBatchPreview;
$('#launch-core').onchange=clearRuntimeHintBatchPreview;
$('#launch-arguments').oninput=clearRuntimeHintBatchPreview;
$('#preview-runtime-hint-batch').onclick=previewRuntimeHintBatch;
$('#apply-runtime-hint').onclick=async()=>{const hint=runtimeImportHints.find(item=>item.id===selectedRuntimeHintID),button=$('#apply-runtime-hint');if(!hint)return;const deviceID=$('#launch-device').value,driverID=$('#launch-driver').value;if(!deviceID||!driverID){toast(tr('请选择目标设备和模拟器驱动'),true);return}const device=deviceProfiles.find(item=>item.id===deviceID),frontendID=frontendAdapters.some(item=>item.id===hint.frontend_adapter_id&&item.enabled)?hint.frontend_adapter_id:device?.default_frontend_id||'',payload={edition_id:editingEditionID,device_profile_id:deviceID,driver_id:driverID,frontend_adapter_id:frontendID,core_id:$('#launch-core').value,arguments:$('#launch-arguments').value.split(/\r?\n/).map(item=>item.trim()).filter(Boolean),enabled:true};button.disabled=true;try{await api(`/api/runtime-import-hints/${encodeURIComponent(hint.id)}/apply`,{method:'POST',body:JSON.stringify(payload)});[launchBindings,runtimeImportHints]=await Promise.all([api('/api/launch-bindings'),api('/api/runtime-import-hints')]);syncLaunchEditor();const found=findEdition(editingEditionID);if(found)renderRuntimeImportHints(found.edition);toast(tr('导入建议已审核并应用'))}catch(e){toast(e.message,true)}finally{button.disabled=false}};
$('#save-launch-binding').onclick=async()=>{const button=$('#save-launch-binding');button.disabled=true;try{await persistLaunchBinding()}catch(e){toast(e.message,true)}finally{button.disabled=false}};
$('#preview-launch-binding').onclick=async()=>{const button=$('#preview-launch-binding');button.disabled=true;try{await persistLaunchBinding(false);const result=await api(`/api/launch-bindings/resolve?edition_id=${encodeURIComponent(editingEditionID)}&device_profile_id=${encodeURIComponent($('#launch-device').value)}`),core=result.core_resolution?.core?.name||tr('不需要核心'),argv=result.arguments.map(item=>`<code>${esc(item)}</code>`).join(' ');$('#launch-preview').innerHTML=`<strong>${tr('启动解析预览')}</strong><small>${esc(result.driver.name)} · ${esc(core)} · ${esc(result.rom_path)}</small><div>${argv}</div>${result.warnings?.length?`<p>${result.warnings.map(esc).join('<br>')}</p>`:''}`;$('#launch-preview').hidden=false}catch(e){toast(e.message,true)}finally{button.disabled=false}};
$('#delete-launch-binding').onclick=async()=>{const binding=selectedLaunchBinding();if(!binding||!confirm(tr('只移除这条启动绑定；不会删除 ROM、模拟器或配置文件。')))return;try{await api(`/api/launch-bindings/${binding.id}`,{method:'DELETE'});launchBindings=await api('/api/launch-bindings');syncLaunchEditor();toast(tr('启动绑定已移除'))}catch(e){toast(e.message,true)}};
$('#save-device').onchange=syncSaveEditor;
$('#save-driver').onchange=()=>{refreshSaveCores();applySaveDriverDefaults()};
$('#save-core').onchange=applySaveDriverDefaults;
$('#save-owner-type').onchange=updateSaveOwnerField;
$('#save-save-binding').onclick=async()=>{const button=$('#save-save-binding');button.disabled=true;try{await persistSaveBinding()}catch(e){toast(e.message,true)}finally{button.disabled=false}};
$('#delete-save-binding').onclick=async()=>{const binding=selectedSaveBinding();if(!binding||!confirm(tr('只移除自动同步绑定；不会删除存档历史或设备上的文件。')))return;try{await api(`/api/save-bindings/${binding.id}`,{method:'DELETE'});saveBindings=await api('/api/save-bindings');syncSaveEditor();toast(tr('自动存档绑定已移除'))}catch(e){toast(e.message,true)}};
$('#recheck').onclick=async()=>{try{const r=await api('/api/artifacts/recheck',{method:'POST',body:'{}'});toast(`已检查 ${r.checked} 个文件，缺失 ${r.missing} 个`);await load()}catch(e){toast(e.message,true)}};

function switchView(id){
  document.querySelectorAll('.view').forEach(v=>v.hidden=v.id!==id);document.querySelectorAll('[data-view]').forEach(n=>n.classList.toggle('active',n.dataset.view===id));
  const library=id==='library-view';$('#new-game').hidden=!library;search.parentElement.hidden=!library;
  window.scrollTo(0,0);
}
document.querySelectorAll('a[data-view]').forEach(link=>link.onclick=e=>{e.preventDefault();switchView(link.dataset.view);history.replaceState(null,'',link.getAttribute('href'))});

const runtimeNames={web:'生态中有 Web 模拟器',web_experimental:'Web 模拟器生态支持有限',native:'需要本机模拟器'};
function platformRuntimeState(platform){
  if(webPlayablePlatforms.has(String(platform?.id||'').toLowerCase()))return webEmulatorReadiness?.enabled?{label:'浏览器可运行',className:'web-ready'}:{label:'浏览器未启用',className:'web-disabled'};
  if(platform?.runtime==='web'||platform?.runtime==='web_experimental')return{label:'仅外部 Web 方案',className:'web-external'};
  return{label:runtimeNames[platform?.runtime]||platform?.runtime||'',className:'native'};
}
const biosNames={none:'无需 BIOS',optional:'BIOS 可选',required:'需要 BIOS',varies:'依具体游戏而定'};
const platformCategoryNames={console:'家用主机',handheld:'便携掌机',arcade:'街机系统',computer:'电脑平台'};
const platformThemes={Atari:'atari',Arcade:'arcade',Computer:'computer',Apple:'computer',Commodore:'computer',Amstrad:'computer',Sinclair:'computer',MSX:'computer',Sharp:'computer',Fujitsu:'computer','The 3DO Company':'threedo',Nintendo:'nintendo',NEC:'nec',Sega:'sega',SNK:'snk',Sony:'sony',Bandai:'bandai',Microsoft:'microsoft',Coleco:'classic',Mattel:'classic',GCE:'classic',Philips:'classic',Lexaloffle:'pico'};
const platformGlyphs={nes:'FC',snes:'SFC',n64:'N64',famicomdisk:'FDS',n64dd:'64DD',gamecube:'GC',wii:'Wii',wiiu:'Wii U',switch:'NS',gb:'GB',gbc:'GBC',gba:'GBA',nds:'DS','3ds':'3DS',virtualboy:'VB',gameandwatch:'G&W',pokemini:'MINI',pcengine:'PCE',pcenginecd:'PCE CD',supergrafx:'SGX',pcfx:'PC-FX',mastersystem:'SMS',sg1000:'SG',megadrive:'MD',segacd:'M-CD',sega32x:'32X',gamegear:'GG',saturn:'SS',dreamcast:'DC',neogeo:'AES',neogeocd:'NG CD',ngpc:'NGPC',psx:'PS1',ps2:'PS2',ps3:'PS3',psp:'PSP',psvita:'VITA',xbox:'XBOX',xbox360:'X360',wonderswan:'WS',wonderswancolor:'WSC',arcade:'ARCADE',naomi:'NAOMI',atomiswave:'AW',dos:'DOS',scummvm:'SCUMM',atari8bit:'A8',atarist:'ST',jaguarcd:'JAG CD',apple2:'AII',amiga:'AMIGA',amigacd32:'CD32',c64:'C64',amstradcpc:'CPC',zxspectrum:'ZX',msx:'MSX',msx2:'MSX2',pc88:'PC-88',pc98:'PC-98',x68000:'X68K',fmtowns:'TOWNS',colecovision:'CV',intellivision:'INTV',vectrex:'VEC',cdi:'CD-i',pico8:'PICO-8'};
function platformVisualKind(platform){if(['nds','3ds','wiiu'].includes(platform.id))return'dual';if(platform.id==='switch')return'hybrid';if(platform.category==='arcade')return'arcade';if(platform.category==='computer')return'computer';if(platform.category==='handheld')return'handheld';if(['3do','jaguarcd','amigacd32','cdi','gamecube','wii','wiiu','pcenginecd','pcfx','segacd','saturn','dreamcast','neogeocd','psx','ps2','ps3','xbox','xbox360'].includes(platform.id))return'disc';return'console'}
function platformDeviceIcon(platform){
  const common='viewBox="0 0 96 76" aria-hidden="true" focusable="false"';
  const icons={
    console:`<svg ${common}><path d="M10 23l5-5h66l5 5v35H10Z"/><path d="M20 30h28M20 38h18"/><path d="M57 28h18v14H57z"/><circle cx="65" cy="35" r="2"/><path d="M25 58l-5 9m51-9 5 9"/></svg>`,
    disc:`<svg ${common}><path d="M10 23l5-5h66l5 5v36H10Z"/><circle cx="35" cy="38.5" r="12"/><circle cx="35" cy="38.5" r="3"/><path d="M56 31h19M56 38h13M56 45h16"/><path d="m77 48-4 4h4z"/></svg>`,
    handheld:`<svg ${common}><path d="M13 14h70l6 9v31l-8 9H15l-8-9V23Z"/><path d="M30 21h36v25H30zM19 43h12m-6-6v12"/><path d="m76 35 4 4-4 4-4-4Zm-6 8 4 4-4 4-4-4Z"/></svg>`,
    dual:`<svg ${common}><path d="m18 11 5-5h50l5 5v24H18Zm0 35 5-5h50l5 5v24H18Z"/><path d="M26 12h44v17H26zm1 35h42v15H27zm4-12v6m34-6v6"/></svg>`,
    hybrid:`<svg ${common}><path d="m21 17 5-5h44l5 5v47H21Z"/><path d="M27 18h42v40H27zM21 17H13l-5 8v27l5 7h8m54-42h8l5 8v27l-5 7h-8"/><path d="M13 32h8m-4-4v8"/><path d="m81 27 4 4-4 4-4-4Zm-1 15 3 3-3 3-3-3Z"/></svg>`,
    arcade:`<svg ${common}><path d="M25 5h46l8 21-7 44H24l-7-44 8-21Z"/><path d="M28 12h40v24H28zM23 44h51M35 51h11m-5-5v11"/><path d="m61 47 4 4-4 4-4-4Zm7 6 3 3-3 3-3-3ZM30 70v-9m36 9v-9"/></svg>`,
    computer:`<svg ${common}><path d="m14 12 5-5h58l5 5v40H14Z"/><path d="M21 14h54v30H21zM42 52v8m12-8v8M30 61h36M19 66h58l6 6H13Z"/></svg>`
  };
  return icons[platformVisualKind(platform)];
}
function platformByValue(value){value=String(value||'').toLowerCase();return platformPresets.find(p=>p.id===value||p.aliases?.includes(value)||p.esde_systems?.includes(value))}
function platformOptions(){
  const groups=new Map();platformPresets.forEach(p=>{if(!groups.has(p.vendor))groups.set(p.vendor,[]);groups.get(p.vendor).push(p)});
  return '<option value="">选择游戏平台…</option>'+[...groups].map(([vendor,items])=>`<optgroup label="${esc(vendor)}">${items.map(p=>`<option value="${esc(p.id)}">${esc((locale.value==='en'||locale.value==='ja')?p.name:(p.name_zh||p.name))} · ${esc(p.id)}</option>`).join('')}</optgroup>`).join('')+'<option value="__custom">添加自定义平台…</option>';
}
function platformHelp(platform){if(!platform)return '选择平台后显示兼容格式和运行方式';const extensions=(platform.extensions||[]).slice(0,6).join(' '),runtime=platformRuntimeState(platform).label,bios=biosNames[platform.bios]||platform.bios;return `${tr(runtime)} · ${tr(bios)} · ${extensions}`}
function setPlatformChoice(kind,value){
  const select=$(`#${kind}-platform-preset`),input=$(`#${kind}-platform-custom`),help=$(`#${kind}-platform-help`),preset=platformByValue(value);
  if(!select||!input)return;
  if(preset){select.value=preset.id;input.value=preset.id;input.hidden=true;input.required=false;help.textContent=platformHelp(preset)}
  else if(value){select.value='';input.value=value;input.hidden=false;input.required=true;help.textContent='这是尚未登记的平台标识；可以保留，也可以在设置中补齐 ROM 格式与前端映射。'}
  else{select.value='';input.value='';input.hidden=true;input.required=false;help.textContent='选择平台后显示兼容格式和运行方式'}
}
function syncPlatformChoice(kind){const select=$(`#${kind}-platform-preset`);if(select.value==='__custom'){select.value='';openCustomPlatform();return}setPlatformChoice(kind,select.value)}
function refreshPlatformOptions(){
  const gameValue=$('#game-platform-custom')?.value||$('#game-platform-preset')?.value||'',importValue=$('#import-platform-custom')?.value||$('#import-platform-preset')?.value||'',options=platformOptions();
  $('#game-platform-preset').innerHTML=options;$('#import-platform-preset').innerHTML=options;setPlatformChoice('game',gameValue);setPlatformChoice('import',importValue);
}
async function loadPlatformPresets(){
  try{[platformPresets,customPlatforms]=await Promise.all([api('/api/platforms'),api('/api/custom-platforms')]);refreshPlatformOptions();$('#platform-total').textContent=platformPresets.length;renderPlatformCatalog()}catch(e){toast(`${tr('平台注册表加载失败')}：${e.message}`,true)}
}
$('#game-platform-preset').onchange=()=>syncPlatformChoice('game');
$('#import-platform-preset').onchange=()=>syncPlatformChoice('import');
let platformCategory='all';
function platformTargetIcon(key){
  const common='viewBox="0 0 24 24" aria-hidden="true" focusable="false"';
  const icons={
    windows:`<svg ${common}><path d="M3 5.5 10.5 4v7H3Zm10-1.9L21 2v9h-8ZM3 13h7.5v7L3 18.5Zm10 0h8v9l-8-1.6Z"/></svg>`,
    android:`<svg ${common}><path d="m7 7-2-3m12 3 2-3M6 9h12v8H6Zm2 8v4m8-4v4M4 10v6m16-6v6"/><path d="M9 6h6l3 3H6Z"/><circle cx="10" cy="10" r=".7"/><circle cx="14" cy="10" r=".7"/></svg>`,
    handheld_linux:`<svg ${common}><path d="m4 7 2-2h12l2 2 2 11-2 2-5-4H9l-5 4-2-2Z"/><path d="M7 9v5m-2.5-2.5h5M16 10h.1m2 2h.1"/></svg>`
  };
  return icons[key]||'';
}
function platformTarget(platform,key,label){const values=platform.suggested_emulators?.[key]||[];return `<span class="${values.length?'has-suggestion':'no-suggestion'}"><i>${platformTargetIcon(key)}</i><b>${label}</b><em>${esc(values.join(' / ')||'暂无稳定建议')}</em></span>`}
function platformRow(platform){
  const allExtensions=platform.extensions||[],extensions=allExtensions.slice(0,4).map(x=>`<code>${esc(x)}</code>`).join('')+(allExtensions.length>4?`<code>+${allExtensions.length-4}</code>`:'');
  const theme=platformThemes[platform.vendor]||'neutral',glyph=platformGlyphs[platform.id]||platform.id.toUpperCase();
  const localizedName=(locale.value==='en'||locale.value==='ja')?platform.name:(platform.name_zh||platform.name),action=platform.builtin?`<button type="button" data-use-platform="${esc(platform.id)}" aria-label="${esc(tr('用于新游戏'))}：${esc(localizedName)}" title="${esc(tr('用于新游戏'))}"><span>用于新游戏</span><b>${uiIcon('add')}</b></button>`:`<button type="button" data-edit-platform="${esc(platform.id)}" aria-label="${esc(tr('编辑平台'))}：${esc(localizedName)}" title="${esc(tr('编辑平台'))}"><span>编辑平台</span><b>${uiIcon('more')}</b></button>`;
  const customState=!platform.builtin?`<span class="custom-platform-badge">${platform.enabled?'自定义':'已停用'}</span>`:'';
  const aliases=platform.aliases?.length?`${tr('别名')}：${esc(platform.aliases.join(', '))}`:tr('使用规范平台 ID');
  const frontend=(platform.esde_systems||[])[0]||platform.id,runtime=platformRuntimeState(platform);
  return `<article class="platform-row theme-${theme} ${platform.builtin?'':'custom-platform'} ${platform.enabled?'':'is-disabled'}" data-platform-id="${esc(platform.id)}"><div class="platform-mark">${platformDeviceIcon(platform)}<b>${esc(glyph)}</b></div><div class="platform-identity"><div><span class="platform-vendor">${esc(platform.vendor)}</span><code>${esc(platform.id)}</code>${customState}</div><h3>${esc(localizedName)}</h3><small>${esc(platformCategoryNames[platform.category]||platform.category)}${localizedName!==platform.name?` · ${esc(platform.name)}`:''}</small></div><div class="platform-spec"><span class="platform-cell-label">ROM / 前端</span><div class="extension-list">${extensions||`<code>—</code>`}</div><small>ES-DE · ${esc(frontend)}</small></div><div class="platform-runtime-cell"><div class="platform-runtime-summary"><span class="runtime-badge ${esc(runtime.className)}"><i></i>${esc(tr(runtime.label))}</span><strong>${esc(biosNames[platform.bios]||platform.bios)}</strong></div><details class="platform-compat"><summary><span class="platform-target-markers" aria-hidden="true">${platformTargetIcon('windows')}${platformTargetIcon('android')}${platformTargetIcon('handheld_linux')}</span><b>${esc(tr('兼容详情'))}</b><i aria-hidden="true"></i></summary><div class="platform-compat-body"><p><strong>${esc(tr('前端目录与别名'))}</strong><span>${esc(frontend)} · ${aliases}</span></p><div class="platform-targets">${platformTarget(platform,'windows','Windows')}${platformTarget(platform,'android','Android')}${platformTarget(platform,'handheld_linux','掌机 Linux')}</div></div></details></div><div class="platform-row-actions">${action}</div></article>`;
}
function renderPlatformCatalog(){
  if(!$('#platform-grid'))return;const q=$('#platform-search').value.trim().toLowerCase(),catalogItems=[...platformPresets,...customPlatforms.filter(custom=>!platformPresets.some(active=>active.id===custom.id))],shown=catalogItems.filter(p=>(platformCategory==='all'||p.category===platformCategory)&&(!q||[p.id,p.name,p.name_zh,p.vendor,...(p.aliases||[]),...(p.extensions||[]),...(p.esde_systems||[])].join(' ').toLowerCase().includes(q)));
  $('#platform-grid').innerHTML=`<header class="platform-directory-head"><span></span><span>平台名称</span><span>ROM / 前端</span><span>运行与模拟器</span><span>操作</span></header>${shown.map(platformRow).join('')}`;$('#platform-grid').hidden=shown.length===0;$('#platform-empty').hidden=shown.length!==0;
  document.querySelectorAll('[data-use-platform]').forEach(button=>button.onclick=()=>{openNewGame();setPlatformChoice('game',button.dataset.usePlatform)});
  document.querySelectorAll('[data-edit-platform]').forEach(button=>button.onclick=()=>openCustomPlatform(button.dataset.editPlatform));
}
$('#platform-search').oninput=renderPlatformCatalog;
document.querySelectorAll('[data-platform-category]').forEach(button=>button.onclick=()=>{platformCategory=button.dataset.platformCategory;document.querySelectorAll('[data-platform-category]').forEach(x=>x.classList.toggle('active',x===button));renderPlatformCatalog()});

const csvValues=value=>String(value||'').split(',').map(item=>item.trim()).filter(Boolean);
function openCustomPlatform(id=''){
  editingCustomPlatformID=id;const dialog=$('#platform-editor-dialog'),form=$('#platform-editor-form'),item=customPlatforms.find(platform=>platform.id===id);form.reset();
  $('#platform-editor-title').textContent=item?tr('编辑自定义平台'):tr('添加自定义平台');form.elements.id.disabled=!!item;$('#delete-custom-platform').hidden=!item;
  if(item){for(const key of ['id','name','name_zh','vendor','category','bios','runtime'])setNamed(form,key,item[key]);setNamed(form,'aliases',(item.aliases||[]).join(', '));setNamed(form,'extensions',(item.extensions||[]).join(', '));setNamed(form,'esde_systems',(item.esde_systems||[]).join(', '));setNamed(form,'emulators_windows',(item.suggested_emulators?.windows||[]).join(', '));setNamed(form,'emulators_android',(item.suggested_emulators?.android||[]).join(', '));setNamed(form,'emulators_handheld_linux',(item.suggested_emulators?.handheld_linux||[]).join(', '));form.elements.enabled.checked=item.enabled}else{form.elements.enabled.checked=true;setNamed(form,'bios','varies');setNamed(form,'runtime','native')}
  dialog.showModal();
}
$('#new-custom-platform').onclick=()=>openCustomPlatform();
$('#platform-editor-form').onsubmit=async event=>{
  event.preventDefault();const form=event.currentTarget,data=new FormData(form),suggested={windows:csvValues(data.get('emulators_windows')),android:csvValues(data.get('emulators_android')),handheld_linux:csvValues(data.get('emulators_handheld_linux'))};
  const input={id:editingCustomPlatformID||String(data.get('id')).trim(),name:String(data.get('name')).trim(),name_zh:String(data.get('name_zh')).trim(),vendor:String(data.get('vendor')).trim(),category:data.get('category'),aliases:csvValues(data.get('aliases')),extensions:csvValues(data.get('extensions')),esde_systems:csvValues(data.get('esde_systems')),bios:data.get('bios'),runtime:data.get('runtime'),suggested_emulators:suggested,enabled:data.get('enabled')==='on'};
  try{await api(editingCustomPlatformID?`/api/custom-platforms/${encodeURIComponent(editingCustomPlatformID)}`:'/api/custom-platforms',{method:editingCustomPlatformID?'PUT':'POST',body:JSON.stringify(input)});form.closest('dialog').close();await loadPlatformPresets();toast(tr(editingCustomPlatformID?'自定义平台已更新':'自定义平台已创建'))}catch(error){toast(error.message,true)}
};
$('#delete-custom-platform').onclick=async()=>{
  if(!editingCustomPlatformID||!confirm(tr('确定删除这个未被引用的自定义平台吗？资料库中的游戏和来源不会被删除。')))return;
  try{await api(`/api/custom-platforms/${encodeURIComponent(editingCustomPlatformID)}`,{method:'DELETE'});$('#platform-editor-dialog').close();await loadPlatformPresets();toast(tr('自定义平台已删除'))}catch(error){toast(error.message,true)}
};

async function loadPackageProfiles(){
  try{
    packageProfiles=await api('/api/package-profiles');
    $('#package-profile').innerHTML=targetProfiles().map(p=>`<option value="${esc(p.target)}">${esc(targetNames[p.target]||p.target)}</option>`).join('');
    renderProfileGrid();renderProfile(true);
  }catch(e){toast(e.message,true)}
}
async function loadConfigTemplatePresets(){
  try{configTemplatePresets=await api('/api/config-template-presets');renderConfigTemplatePresets()}catch(e){toast(e.message,true)}
}
const targetNames={rocknix:'ROCKNIX 掌机',android:'Android 掌机',windows:'Windows 掌机','steamos-bazzite':'SteamOS / Bazzite 掌机',darkos:'dArkOS 掌机',arkos:'ArkOS 掌机（遗留兼容）',knulli:'KNULLI 掌机',muos:'muOS 掌机',onionos:'OnionOS 掌机',portable:'便携目录'};
const targetDescriptions={rocknix:'Linux 掌机系统 · 适合 SD 卡部署',android:'Android · Pegasus 或 ES-DE',windows:'Windows · Pegasus 或 ES-DE','steamos-bazzite':'Steam Deck 类 x86-64 掌机 · ES-DE',darkos:'当前维护 · EmulationStation XML',arkos:'已停止维护 · 仅兼容旧设备',knulli:'EmulationStation 系掌机 · XML 元数据',muos:'仅设备 Agent 包 · 尚无原生前端导出',onionos:'仅设备 Agent 包 · 尚无原生前端导出',portable:'跨设备 · 可移动完整副本'};
const modeNames={copy:'复制 ROM',hardlink:'硬链接',reference:'相对路径元数据'};
const localeNames={'zh-CN':'简体中文','zh-TW':'繁體中文',ja:'日本語',en:'English'};
const targetOrder=['android','windows','steamos-bazzite','rocknix','darkos','knulli','portable','arkos'];
function targetProfiles(){return [...new Map(packageProfiles.filter(p=>p.enabled!==false).map(p=>[p.target,p])).values()].sort((a,b)=>{const ai=targetOrder.indexOf(a.target),bi=targetOrder.indexOf(b.target);return (ai<0?targetOrder.length:ai)-(bi<0?targetOrder.length:bi)||String(a.target).localeCompare(String(b.target))})}
function selectedFrontend(){return document.querySelector('input[name="package-frontend"]:checked')?.value||'es-de'}
function renderFrontendChoice(){document.querySelectorAll('.frontend-option').forEach(option=>{const active=option.querySelector('input').checked;option.classList.toggle('active',active)})}
function packageName(target,frontend,locale){return `${target}-${frontend}-${String(locale).toLowerCase()}`}
function profileIcon(target){
  const icons={
    rocknix:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16l2 4-2 6H4l-2-6zM7 12h4M9 10v4M16 11h2M17 10v2"/></svg>',
    android:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M7 3h10l2 3v15H5V6zM8 7h8M10 3 8 1M14 3l2-2M10 17h4"/></svg>',
    windows:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 5 11 3v8H3zM13 3l8-2v10h-8zM3 13h8v8l-8-2zM13 13h8v10l-8-2z"/></svg>',
    'steamos-bazzite':'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16l2 4-2 6H4l-2-6zM7 12h4M9 10v4M15 10l3 3M18 10l-3 3"/></svg>',
    darkos:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16l2 4-2 6H4l-2-6zM7 12h4M9 10v4M15 12l2 2 3-4"/></svg>',
    arkos:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16l2 4-2 6H4l-2-6zM7 12h4M9 10v4M15 11h2M18 13h2"/></svg>',
    knulli:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 7h16l2 4-2 6H4l-2-6zM7 12h4M9 10v4M15 10l4 4M19 10l-4 4"/></svg>',
    portable:'<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3 6h7l2 2h9v11H3zM11 13h7M15 10l3 3-3 3"/></svg>'
  };return icons[target]||icons.portable;
}
function renderProfileGrid(){
  const targets=targetProfiles(),selected=$('#package-profile').value||targets[0]?.target;
  $('#profile-grid').innerHTML=targets.map(p=>{const name=tr(targetNames[p.target]||p.target),description=tr(targetDescriptions[p.target]||p.file_mode);return `<button type="button" class="profile-option ${p.target===selected?'active':''}" data-profile="${esc(p.target)}" role="radio" aria-checked="${p.target===selected}" title="${esc(`${name} — ${description}`)}"><span class="profile-icon">${profileIcon(p.target)}</span><span class="profile-check">${uiIcon('check')}</span><strong>${esc(name)}</strong><small>${esc(description)}</small></button>`}).join('');
  document.querySelectorAll('.profile-option').forEach(button=>button.onclick=()=>{$('#package-profile').value=button.dataset.profile;renderProfileGrid();renderProfile(true)});
}
function renderProfile(resetDefaults=false){
  const target=$('#package-profile').value||targetProfiles()[0]?.target,presets=packageProfiles.filter(p=>p.target===target);if(!presets.length)return;
  if(resetDefaults){const preferred=presets[0];document.querySelector(`input[name="package-frontend"][value="${preferred.frontend}"]`).checked=true;$('#package-locale').value=preferred.locale;$('#package-mode').value=preferred.file_mode;packageTemplateDrafts=(preferred.templates||[]).map(t=>({name:t.name,scope:t.scope,output_path:t.output_path,body:t.body}));renderPackageTemplates()}
  renderFrontendChoice();const frontend=selectedFrontend(),mode=$('#package-mode').value,localeValue=$('#package-locale').value;renderConfigTemplatePresets();
  $('#profile-summary').innerHTML=`<div><span>目标设备</span><strong>${esc(targetNames[target]||target)}</strong></div><div><span>前端格式</span><strong>${esc(frontend==='es-de'?'ES-DE':'Pegasus')}</strong></div><div><span>ROM 策略</span><strong id="summary-mode">${esc(tr(modeNames[mode]||mode))}</strong></div>`;
  renderReferenceModeNote(mode);
  $('#package-destination').textContent=`state/exports/${packageName(target,frontend,localeValue)}`;$('#package-result').hidden=true;$('#confirm-package-build').hidden=true;lastPackagePlanID='';setInlineState('#package-inline-state','');
}
function setInlineState(selector,message,type='') {const el=$(selector);el.textContent=message;el.className=`inline-state ${type}`.trim()}
function invalidatePackagePlanUI(){lastPackagePlanID='';$('#package-result').hidden=true;$('#confirm-package-build').hidden=true}
function renderReferenceModeNote(mode){const note=$('#reference-mode-note');if(note)note.hidden=mode!=='reference'}
function compatibleTemplatePresets(){const target=$('#package-profile').value,frontend=selectedFrontend();return configTemplatePresets.filter(item=>(item.targets||[]).includes(target)&&(item.frontends||[]).includes(frontend))}
function renderConfigTemplatePresets(){
  const list=$('#template-preset-list');if(!list)return;const presets=compatibleTemplatePresets();
  list.innerHTML=presets.map(preset=>{const copied=packageTemplateDrafts.some(item=>item.output_path===preset.output_path);return `<article class="template-preset-row" data-template-preset="${esc(preset.id)}"><span class="template-preset-mark" aria-hidden="true">${uiIcon('config')}</span><span class="template-preset-copy"><strong>${esc(tr(preset.name))}</strong><small>${esc(tr(preset.summary))}</small><em>${esc((preset.requires||[]).map(tr).join(' · '))}</em></span><button type="button" ${copied?'disabled':''}><span>${tr(copied?'已复制':'复制并编辑')}</span><b aria-hidden="true">${uiIcon(copied?'check':'add')}</b></button></article>`}).join('')||`<div class="template-preset-empty">${tr('当前设备与前端没有匹配的内置模板；仍可从空白开始。')}</div>`;
  list.querySelectorAll('[data-template-preset] button:not(:disabled)').forEach(button=>button.onclick=()=>{const preset=configTemplatePresets.find(item=>item.id===button.closest('[data-template-preset]').dataset.templatePreset);if(!preset)return;packageTemplateDrafts.push({name:tr(preset.name),scope:preset.scope,output_path:preset.output_path,body:preset.body});renderPackageTemplates();invalidatePackagePlanUI();toast(tr('模板已复制，可以继续编辑'))})
}
function renderPackageTemplates(){
  $('#template-count').textContent=`${packageTemplateDrafts.length} 个模板`;$('#package-template-list').innerHTML=packageTemplateDrafts.map((template,index)=>`<article class="package-template" data-template-index="${index}"><header><input data-template-field="name" value="${esc(template.name)}" placeholder="模板名称" aria-label="模板名称"><select data-template-field="scope" aria-label="模板范围"><option value="package" ${template.scope==='package'?'selected':''}>整合包一次</option><option value="platform" ${template.scope==='platform'?'selected':''}>每个平台</option><option value="edition" ${template.scope==='edition'?'selected':''}>每个游戏版本</option></select><button type="button" class="template-remove" aria-label="移除模板">${uiIcon('close')}</button></header><input data-template-field="output_path" value="${esc(template.output_path)}" placeholder="config/{{platform.id}}/options.cfg" aria-label="输出相对路径"><textarea data-template-field="body" rows="4" placeholder="rom={{rom.path}}">${esc(template.body)}</textarea></article>`).join('')||'<div class="template-empty">没有自定义模板；前端元数据仍会正常生成。</div>';
  globalThis.uiI18n?.apply($('#template-count'),false);globalThis.uiI18n?.apply($('#package-template-list'),false);
  document.querySelectorAll('[data-template-index]').forEach(row=>{const index=Number(row.dataset.templateIndex);row.querySelectorAll('[data-template-field]').forEach(control=>control.oninput=()=>{packageTemplateDrafts[index][control.dataset.templateField]=control.value;renderConfigTemplatePresets();invalidatePackagePlanUI()});row.querySelector('.template-remove').onclick=()=>{packageTemplateDrafts.splice(index,1);renderPackageTemplates();invalidatePackagePlanUI()}});renderConfigTemplatePresets()
}
$('#add-package-template').onclick=()=>{packageTemplateDrafts.push({name:tr('模拟器配置'),scope:'edition',output_path:'config/{{platform.id}}/{{edition.id}}.cfg',body:'rom={{rom.path}}\n'});renderPackageTemplates();invalidatePackagePlanUI()};
function currentPackageProfile(){const target=$('#package-profile').value,frontend=selectedFrontend(),base=packageProfiles.find(p=>p.target===target&&p.frontend===frontend)||packageProfiles.find(p=>p.target===target);if(!base)return null;const localeValue=$('#package-locale').value,name=packageName(target,frontend,localeValue);return {name,frontend,target,locale:localeValue,file_mode:$('#package-mode').value,output_slug:name,enabled:true,templates:packageTemplateDrafts.map(t=>({...t}))}}
function packageProfileMatchesDraft(saved,draft){const templates=value=>(value||[]).map(item=>({name:item.name,scope:item.scope,output_path:item.output_path,body:item.body}));return saved.name===draft.name&&saved.frontend===draft.frontend&&saved.target===draft.target&&saved.locale===draft.locale&&saved.file_mode===draft.file_mode&&saved.output_slug===draft.output_slug&&JSON.stringify(templates(saved.templates))===JSON.stringify(templates(draft.templates))}
function editablePackageProfileSlug(base){let suffix='-custom',candidate=`${base}${suffix}`,index=2;while(packageProfiles.some(item=>item.builtin&&item.output_slug===candidate)){candidate=`${base}${suffix}-${index++}`}return candidate}
async function ensurePackageProfile(profile){let saved=packageProfiles.find(item=>item.output_slug===profile.output_slug);if(saved?.builtin){if(packageProfileMatchesDraft(saved,profile))return saved;profile={...profile,output_slug:editablePackageProfileSlug(profile.output_slug)};saved=packageProfiles.find(item=>item.output_slug===profile.output_slug)}if(saved){saved=await api(`/api/package-profiles/${encodeURIComponent(saved.id)}`,{method:'PUT',body:JSON.stringify(profile)});packageProfiles=packageProfiles.map(item=>item.id===saved.id?saved:item)}else{saved=await api('/api/package-profiles',{method:'POST',body:JSON.stringify(profile)});packageProfiles.push(saved)}return saved}
function renderPackagePlan(response){
  const plan=response.plan,counts={};(plan.items||[]).forEach(item=>counts[item.action]=(counts[item.action]||0)+1);const conflicts=plan.conflicts||[],warnings=plan.warnings||[];
  const conflictItems=(plan.items||[]).filter(item=>item.action==='conflict'),conflictLines=conflictItems.length?conflictItems.map(item=>`${item.target} · ${tr(item.detail||'构建目标存在冲突')}`):conflicts;
  const estimated=Number(plan.estimated_write_bytes||0),available=Number(plan.available_bytes||0),spaceState=plan.space_checked?compactBytes(available):tr('空间检查未完成');
  $('#package-result').hidden=false;$('#package-result').innerHTML=`<div class="plan-head"><span>${tr('构建计划')}</span><strong>${tr(conflicts.length?'需要先处理冲突':'可以安全构建')}</strong><code>${esc(plan.fingerprint.slice(0,12))}</code></div><div class="result-metrics plan-metrics"><span><b>${(counts.copy||0)+(counts.hardlink||0)}</b><small>${tr('将写入')}</small></span><span><b>${counts.unchanged||0}</b><small>${tr('保持不变')}</small></span><span><b>${counts.generate||0}</b><small>${tr('生成配置')}</small></span><span><b>${counts.missing||0}</b><small>${tr('缺失跳过')}</small></span><span><b>${counts.conflict||0}</b><small>${tr('冲突阻止')}</small></span></div><div class="plan-space"><span><small>${tr('预计写入空间')}</small><strong>${compactBytes(estimated)}</strong></span><span><small>${tr('目标可用空间')}</small><strong>${esc(spaceState)}</strong></span><p>${tr('空间值包含安全余量；确认构建时仍会重新验证来源和目标。')}</p></div>${conflicts.length?`<div class="result-warnings conflict-list"><strong>${tr('以下问题会阻止构建')}</strong><br>${conflictLines.slice(0,8).map(esc).join('<br>')}</div>`:''}${warnings.length?`<div class="result-warnings"><strong>${tr('需要留意')}</strong><br>${warnings.map(w=>esc(tr(w))).join('<br>')}</div>`:''}`;
  $('#confirm-package-build').hidden=conflicts.length>0;
}
$('#package-profile').onchange=()=>{renderProfileGrid();renderProfile(true)};
document.querySelectorAll('input[name="package-frontend"]').forEach(input=>input.onchange=()=>renderProfile());
$('#package-locale').onchange=()=>renderProfile();
$('#package-mode').onchange=()=>{const mode=$('#package-mode').value;const summary=$('#summary-mode');if(summary)summary.textContent=tr(modeNames[mode]||mode);renderReferenceModeNote(mode);invalidatePackagePlanUI()};
$('#package-form').onsubmit=async e=>{
  e.preventDefault();const p=currentPackageProfile();if(!p)return;const button=e.submitter||$('#build-package');
  button.disabled=true;button.classList.add('loading');button.querySelector('span').textContent='正在生成只读计划';setInlineState('#package-inline-state','正在计算来源指纹并检查目标目录；此步骤不会写入整合包…','info');
  try{
    const profile=await ensurePackageProfile(p);$('#package-destination').textContent=`state/exports/${profile.output_slug}`;const r=await api(`/api/package-profiles/${encodeURIComponent(profile.id)}/plans`,{method:'POST',body:'{}'});lastPackagePlanID=r.id;renderPackagePlan(r);setInlineState('#package-inline-state','计划已生成；确认前不会写入任何整合包文件。','info');toast('构建计划已就绪');
  }catch(err){setInlineState('#package-inline-state',err.message,'error');toast(err.message,true)}finally{button.disabled=false;button.classList.remove('loading');button.querySelector('span').textContent='重新生成构建计划'}
};
$('#confirm-package-build').onclick=async()=>{if(!lastPackagePlanID)return;const button=$('#confirm-package-build');button.disabled=true;button.classList.add('loading');button.querySelector('span').textContent='正在安全构建';setInlineState('#package-inline-state','正在重新验证计划并写入受管目标…','info');try{const release=await api(`/api/package-plans/${encodeURIComponent(lastPackagePlanID)}/build`,{method:'POST',body:'{}'}),r=release.result||{},warnings=r.warnings||[],recovery=r.recovery_snapshot||'';$('#package-result').innerHTML=`<div class="result-head"><span class="result-check">${uiIcon('check')}</span><div><strong>整合包已经就绪</strong><code>${esc(r.output||release.output_slug)}</code></div></div><div class="result-metrics"><span><b>${r.exported_editions||0}</b><small>游戏版本</small></span><span><b>${r.copied_files||0}</b><small>新复制</small></span><span><b>${r.linked_files||0}</b><small>硬链接</small></span><span><b>${r.unchanged_files||0}</b><small>未变化</small></span><span><b>${r.missing_files||0}</b><small>缺失</small></span></div>${recovery?`<div class="package-recovery"><strong>${tr('已保留更新前快照')}</strong><code>${esc(recovery)}</code><small>${tr('只包含本次改写前的既有受管文件；不会自动清理。')}</small></div>`:''}${warnings.length?`<div class="result-warnings"><strong>需要留意</strong><br>${warnings.map(w=>esc(tr(w))).join('<br>')}</div>`:''}`;button.hidden=true;setInlineState('#package-inline-state','');toast('整合包生成完成')}catch(err){setInlineState('#package-inline-state',err.message,'error');toast(err.message,true)}finally{button.disabled=false;button.classList.remove('loading');button.querySelector('span').textContent='确认并生成整合包'}};

function compactBytes(size){if(size<1024)return `${size} B`;if(size<1024*1024)return `${(size/1024).toFixed(1)} KB`;return `${(size/1024/1024).toFixed(1)} MB`}
function renderImportSources(){
  const container=$('#detected-sources');$('#detected-count').textContent=importSources.length?`找到 ${importSources.length} 份元数据`:'未检测到可用文件';
  container.innerHTML=importSources.length?importSources.slice(0,6).map((source,index)=>`<button class="detected-source" type="button" data-source-index="${index}"><span>${source.format==='pegasus'?'P':source.format==='es-de'?'ES':'LM'}</span><span><strong>${esc(source.path)}</strong><small>${source.platform?esc(source.platform)+' · ':''}${compactBytes(source.size)}</small></span><b>选择</b></button>`).join(''):'<div class="detected-empty">没有自动找到对应文件。仍可在下方手工输入资料库内的相对路径。</div>';
  document.querySelectorAll('.detected-source').forEach(button=>button.onclick=()=>{const source=importSources[Number(button.dataset.sourceIndex)];if(!source)return;$('#import-form input[name="source"]').value=source.path;if(source.platform)setPlatformChoice('import',source.platform);document.querySelectorAll('.detected-source').forEach(x=>x.classList.toggle('active',x===button));button.querySelector('b').textContent=tr('已选择');setInlineState('#import-inline-state',source.platform?`${tr('已选择')} ${source.path}，并匹配到 ${platformByValue(source.platform)?.name_zh||source.platform}。`:`${tr('已选择')} ${source.path}；${tr('每个条目会保留清单内记录的平台。')}`,'info')});
}
async function loadImportSources(){
	if(document.querySelector('input[name="import_kind"]:checked')?.value!=='metadata')return;
  const format=$('#import-form input[name="format"]:checked').value;$('#detected-count').textContent='正在扫描资料库…';$('#detected-sources').innerHTML='<span class="source-skeleton"></span>';
  try{importSources=await api(`/api/import-sources?format=${encodeURIComponent(format)}`);renderImportSources()}catch(e){importSources=[];$('#detected-count').textContent='扫描失败';$('#detected-sources').innerHTML=`<div class="detected-empty">${esc(e.message)}</div>`}
}
function sourcePath(source){return source.kind==='rom_directory'?source.root_path:source.metadata_path}
function metadataDirectory(path){const parts=String(path||'').split('/').filter(Boolean);parts.pop();return parts.join('/')||'.'}
function sourceLocations(source){const metadata=sourcePath(source);if(source.kind==='rom_directory'||!source.root_path||source.root_path===metadataDirectory(source.metadata_path))return metadata;return `${metadata} · ROM ${source.root_path}`}
function sourceKindLabel(kind){return kind==='rom_directory'?'ROM':kind==='pegasus'?'Pegasus':kind==='esde'?'ES-DE':'Varkiv'}
function currentSourceHandler(){const form=$('#import-form'),metadata=form.elements.import_kind.value==='metadata';if(!metadata)return'rom_directory';return form.elements.format.value==='pegasus'?'pegasus':form.elements.format.value==='es-de'?'esde':'varkiv'}
function renderSourceAdapterChoice(){const select=$('#import-form')?.elements.source_adapter_id;if(!select)return;const handler=currentSourceHandler(),current=select.value,options=sourceAdapters.filter(item=>item.enabled&&item.handler===handler);select.innerHTML=options.map(item=>`<option value="${esc(item.id)}">${esc(item.name)} · ${esc(item.format)}${item.builtin?'':` · ${tr('自定义')}`}</option>`).join('');if(options.some(item=>item.id===current))select.value=current}
function sourceStatusLabel(status){return tr({never:'尚未扫描',ready:'预览待确认',committed:'最近导入成功',failed:'扫描失败',stale:'预览已过期'}[status]||status)}
function renderLibrarySources(){
  const list=$('#source-registry-list');if(!list)return;$('#source-registry-count').textContent=`${librarySources.length} 个来源`;
  list.innerHTML=librarySources.map(source=>{const adapter=sourceAdapters.find(item=>item.id===source.source_adapter_id);return `<article class="saved-source ${source.enabled?'':'disabled'}"><span class="saved-source-kind">${sourceKindLabel(source.kind)}</span><span class="saved-source-copy"><strong>${esc(source.name)}</strong><small>${esc(sourceLocations(source))} · ${esc(source.platform||tr('多平台'))} · ${esc(adapter?.name||source.source_adapter_id)} · ${esc(sourceStatusLabel(source.last_scan_status))}</small></span><span class="saved-source-policy">${tr(source.rom_storage_policy==='copy'?'受管复制':'原位引用')}</span><button class="source-scan" data-source="${esc(source.id)}" ${source.enabled?'':'disabled'}>${tr('重新扫描')}</button><button class="source-toggle" data-source="${esc(source.id)}">${tr(source.enabled?'停用':'启用')}</button></article>`}).join('')||`<div class="source-registry-empty"><span class="source-registry-mark" aria-hidden="true">SRC</span><span><strong>${tr('还没有保存来源')}</strong><small>${tr('在下方选择目录并生成预览，默认会把配置保存在这里。')}</small></span></div>`;
  document.querySelectorAll('.source-scan').forEach(button=>button.onclick=()=>scanSavedSource(button.dataset.source,button));
  document.querySelectorAll('.source-toggle').forEach(button=>button.onclick=()=>toggleSavedSource(button.dataset.source,button));
}
async function loadLibrarySources(){try{[librarySources,sourceScans]=await Promise.all([api('/api/sources'),api('/api/source-scans')]);renderLibrarySources()}catch(e){toast(`来源配置加载失败：${e.message}`,true)}}
function renderHashSources(){
  const list=$('#hash-source-list'),total=$('#hash-source-total strong');if(!list||!total)return;total.textContent=hashSources.length;
  list.innerHTML=hashSources.length?hashSources.map(source=>`<article class="hash-source-chip"><strong>${esc(source.name)}</strong><small>${esc(source.active_version||tr('未发布'))} · ${esc(source.license)} · ${source.release_count} ${tr(source.release_count===1?'个发布':'个发布（复数）')}</small><b>${source.record_count}</b></article>`).join(''):`<p>${tr('还没有导入识别数据。')}</p>`;
}
async function loadHashSources(){try{hashSources=await api('/api/hash-sources');renderHashSources()}catch(e){toast(`${tr('识别库加载失败')}：${e.message}`,true)}}
function resetHashPackPreview(){hashPackPreview=null;const panel=$('#hash-pack-preview');if(panel){panel.hidden=true;panel.innerHTML=''}}
function renderHashPackPreview(){
  const panel=$('#hash-pack-preview');if(!panel||!hashPackPreview){if(panel)panel.hidden=true;return}const p=hashPackPreview,blocked=p.release_conflict||p.existing_release;
  const state=p.release_conflict?tr('发布版本冲突'):p.existing_release?tr('这一发布已经安装'):p.conflict_count?tr('存在来源分歧，既有资料不会被覆盖'):tr('可以安全导入');
  panel.innerHTML=`<header><div><strong>${esc(p.source.name)}</strong><small>${esc(p.source.id)} · ${esc(p.release)} · ${esc(p.source.license)}</small></div><span class="${p.release_conflict||p.conflict_count?'hash-pack-conflict':''}">${esc(state)}</span></header><div class="hash-pack-preview-metrics"><span><b>${p.new_count}</b><small>${esc(tr('新增身份'))}</small></span><span><b>${p.existing_count}</b><small>${esc(tr('已有记录'))}</small></span><span><b>${p.conflict_count}</b><small>${esc(tr('来源分歧'))}</small></span></div><button id="commit-hash-pack" type="button" ${blocked?'disabled':''}>${esc(tr(p.existing_release?'无需重复导入':p.release_conflict?'请更换发布版本':'保留来源并导入'))}</button>`;
  panel.hidden=false;const button=$('#commit-hash-pack');if(button&&!blocked)button.onclick=commitHashPack;
}
async function previewHashPack(){
  if(!hashPackFile){toast(tr('请先选择识别包'),true);return}const button=$('#preview-hash-pack'),body=new FormData();body.append('file',hashPackFile);button.disabled=true;button.textContent=tr('正在检查…');
  try{hashPackPreview=await api('/api/hash-packs/preview',{method:'POST',body});renderHashPackPreview();toast(tr('识别包检查完成'))}catch(e){resetHashPackPreview();toast(e.message,true)}finally{button.disabled=false;button.textContent=tr('检查识别包')}
}
async function commitHashPack(){
  if(!hashPackFile||!hashPackPreview)return;const button=$('#commit-hash-pack'),body=new FormData();body.append('file',hashPackFile);body.append('preview_token',hashPackPreview.preview_token);button.disabled=true;button.textContent=tr('正在导入…');
  try{const result=await api('/api/hash-packs/import',{method:'POST',body});toast(tr('识别库发布已导入'));hashPackFile=null;$('#hash-pack-file').value='';$('#hash-pack-file-name').textContent=tr('尚未选择文件');resetHashPackPreview();await loadHashSources();if(result.existing_release)toast(tr('这一发布已经安装'))}catch(e){button.disabled=false;button.textContent=tr('重新检查后导入');toast(e.message,true)}
}
async function exportHashPack(event){
  event.preventDefault();const form=event.currentTarget,data=Object.fromEntries(new FormData(form)),button=$('#export-hash-pack'),headers={'Content-Type':'application/json'},token=sessionStorage.getItem('game-library-token');if(token)headers.Authorization=`Bearer ${token}`;button.disabled=true;button.querySelector('span').textContent=tr('正在生成…');
  try{const response=await fetch('/api/v1/hash-packs/export',{method:'POST',headers,body:JSON.stringify(data)});if(response.status===401){showAuth();throw new Error(tr('访问令牌无效或已失效'))}if(!response.ok){let message=`HTTP ${response.status}`;try{const body=await response.json();message=apiErrorMessages[body?.error?.code]?tr(apiErrorMessages[body.error.code]):body?.error?.message||message}catch{}throw new Error(message)}const blob=await response.blob(),match=response.headers.get('Content-Disposition')?.match(/filename="([^"]+)"/),name=match?.[1]||`${data.source_id}-${data.release}.hashpack`,url=URL.createObjectURL(blob),link=document.createElement('a');link.href=url;link.download=name;document.body.append(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),1000);toast(tr('识别包已生成并下载'))}catch(e){toast(e.message,true)}finally{button.disabled=false;button.querySelector('span').textContent=tr('生成并下载识别包')}
}
async function scanSavedSource(id,button){
  const source=librarySources.find(item=>item.id===id);if(!source)return;button.disabled=true;button.textContent='扫描中';setInlineState('#import-inline-state',`正在只读扫描 ${source.name}…`,'info');
  try{const r=await api(`/api/sources/${encodeURIComponent(id)}/scans`,{method:'POST',body:'{}'});lastSourceScanID=r.scan.id;lastImportRequest={kind:'persistent',source_id:id};renderImportPreview(r);setInlineState('#import-inline-state','');toast(`已检查 ${r.scan.candidate_count} 个条目，请审查变化`);await loadLibrarySources()}catch(err){setInlineState('#import-inline-state',err.message,'error');toast(err.message,true)}finally{button.disabled=false;button.textContent='重新扫描'}
}
async function toggleSavedSource(id,button){
  const source=librarySources.find(item=>item.id===id);if(!source)return;button.disabled=true;
  try{await api(`/api/sources/${encodeURIComponent(id)}`,{method:'PUT',body:JSON.stringify({name:source.name,kind:source.kind,source_adapter_id:source.source_adapter_id,root_path:source.root_path,metadata_path:source.metadata_path,runtime_metadata_path:source.runtime_metadata_path||'',platform:source.platform,metadata_locale:source.metadata_locale,rom_storage_policy:source.rom_storage_policy,media_storage_policy:source.media_storage_policy,enabled:!source.enabled})});await loadLibrarySources();toast(source.enabled?'来源已停用；历史和源文件均保留':'来源已启用')}catch(err){toast(err.message,true)}finally{button.disabled=false}
}
function persistentSourceBody(request,formData){
  const metadata=request.kind==='metadata',path=metadata?request.source:request.source,leaf=path.split('/').filter(Boolean).pop()||path;
  const neutral=metadata&&request.format==='varkiv',kind=metadata?(request.format==='pegasus'?'pegasus':request.format==='es-de'?'esde':'varkiv'):'rom_directory';
  return {name:String(formData.get('source_name')||'').trim()||`${request.platform||'多平台'} · ${leaf}`,kind,source_adapter_id:String(formData.get('source_adapter_id')||''),root_path:metadata?(request.content_root||''):path,metadata_path:metadata?path:'',runtime_metadata_path:metadata&&request.format==='es-de'?(request.runtime_source||''):'',platform:neutral?'':request.platform,metadata_locale:metadata&&!neutral?request.locale:'',rom_storage_policy:request.rom_storage,media_storage_policy:metadata?request.media_storage:'ignore',enabled:true};
}
function samePersistentSource(left,right){const expectedRoot=right.kind==='rom_directory'?right.root_path:(right.root_path||metadataDirectory(right.metadata_path));return left.kind===right.kind&&left.source_adapter_id===right.source_adapter_id&&left.platform===right.platform&&sourcePath(left)===(right.kind==='rom_directory'?right.root_path:right.metadata_path)&&(left.root_path||'')===expectedRoot&&(left.runtime_metadata_path||'')===(right.runtime_metadata_path||'')}
function syncImportFormat(){
  const form=$('#import-form'),format=form.querySelector('input[name="format"]:checked').value,isPegasus=format==='pegasus',isESDE=format==='es-de',isNeutral=format==='varkiv',input=form.elements.source;
  document.querySelectorAll('.source-option').forEach(label=>label.classList.toggle('active',label.querySelector('input').checked));
  input.value='';input.placeholder=isPegasus?'gba/metadata.pegasus.txt':isESDE?'gamelists/gba/gamelist.xml':'exports/handheld/library-manifest.json';$('#source-example').textContent=input.placeholder;$('#source-file-icon').textContent=isPegasus?'TXT':isESDE?'XML':'JSON';const runtimeField=$('#runtime-source-field'),runtimeInput=form.elements.runtime_source;runtimeField.hidden=!isESDE;runtimeInput.disabled=!isESDE;runtimeInput.value='';
  $('#import-platform-field').hidden=isNeutral;$('#import-platform-preset').disabled=isNeutral;$('#import-platform-preset').required=!isNeutral;form.elements.platform.value=isNeutral?'':form.elements.platform.value;$('#metadata-locale-field').hidden=isNeutral;form.elements.locale.disabled=isNeutral;$('#content-root-field').hidden=isNeutral;form.elements.content_root.disabled=isNeutral;if(isNeutral)form.elements.content_root.value='';const remember=form.elements.remember_source;remember.disabled=false;form.elements.source_name.disabled=false;$('#remember-source-field').hidden=false;renderSourceAdapterChoice();loadImportSources();
	$('#import-empty').hidden=false;$('#import-review').hidden=true;setImportStep(1);setInlineState('#import-inline-state','');lastImportRequest=null;lastImportPreviewToken='';lastImportCandidates=[];renderPortablePlatformChanges();renderPortableRuntimeChanges();
}
function syncImportStorage(){
	const form=$('#import-form'),kind=form.elements.import_kind.value,rom=form.elements.rom_storage.value,media=form.elements.media_storage.value;
  const romNames={reference:'引用现有文件',copy:'复制到受管 ROM 区'},mediaNames={copy:'受管去重存储',reference:'引用现有媒体',ignore:'不导入媒体'};
  $('#rom-storage-help').textContent=rom==='copy'?'原子复制到 state/roms；源文件不会删除。':'零额外占用；NAS 路径离线时会显示缺失。';
	$('#media-storage-help').textContent=media==='copy'?'保存到 state/media；相同 SHA-256 内容只存一份。':media==='reference'?'媒体随原整合包路径使用；移动来源后会失效。':'只导入游戏资料和 ROM 关系。';
	$('#storage-plan').innerHTML=kind==='rom'?`<span>ROM</span><strong>${romNames[rom]}</strong><i>${uiIcon('arrow')}</i><span>元数据</span><strong>从文件名建立，稍后可编辑</strong>`:`<span>ROM</span><strong>${romNames[rom]}</strong><i>${uiIcon('arrow')}</i><span>媒体</span><strong>${mediaNames[media]}</strong>`;
}
function resetImportPreview(){
	$('#import-empty').hidden=false;$('#import-review').hidden=true;$('#import-source-state').hidden=true;$('#import-source-state').innerHTML='';$('#commit-note').textContent=tr('重复和冲突项不会提交');setImportStep(1);setInlineState('#import-inline-state','');lastImportRequest=null;lastImportPreviewToken='';lastSourceScanID='';lastImportCandidates=[];renderPortablePlatformChanges();renderPortableRuntimeChanges();
}
function syncImportKind(){
	const form=$('#import-form'),kind=form.elements.import_kind.value,isMetadata=kind==='metadata';
	document.querySelectorAll('.import-kind-option').forEach(label=>label.classList.toggle('active',label.querySelector('input').checked));
	const neutral=isMetadata&&form.elements.format.value==='varkiv';$('#rom-source-fields').hidden=isMetadata;$('#metadata-source-fields').hidden=!isMetadata;$('#metadata-locale-field').hidden=!isMetadata||neutral;$('#media-storage-field').hidden=!isMetadata;$('#content-root-field').hidden=!isMetadata||neutral;
	form.elements.rom_source.disabled=isMetadata;form.elements.rom_source.required=!isMetadata;form.elements.source.disabled=!isMetadata;form.elements.source.required=isMetadata;form.elements.locale.disabled=!isMetadata;form.elements.media_storage.disabled=!isMetadata;
	form.elements.content_root.disabled=!isMetadata||neutral;
	const esde=isMetadata&&form.elements.format.value==='es-de';$('#runtime-source-field').hidden=!esde;form.elements.runtime_source.disabled=!esde;
	if(!isMetadata){$('#import-platform-field').hidden=false;$('#import-platform-preset').disabled=false;$('#import-platform-preset').required=true;$('#remember-source-field').hidden=false;form.elements.remember_source.disabled=false;form.elements.source_name.disabled=false;if(form.elements.remember_source.dataset.restore){form.elements.remember_source.checked=form.elements.remember_source.dataset.restore==='1';delete form.elements.remember_source.dataset.restore}}
	$('#preview-import span').textContent=isMetadata?'检查元数据并生成预览':'扫描 ROM 并生成预览';
	if(isMetadata)syncImportFormat();else renderSourceAdapterChoice();syncImportStorage();resetImportPreview();
}
document.querySelectorAll('#import-form select[name$="_storage"]').forEach(select=>select.onchange=syncImportStorage);syncImportStorage();
document.querySelectorAll('#import-form input[name="format"]').forEach(input=>input.onchange=syncImportFormat);
document.querySelectorAll('#import-form input[name="import_kind"]').forEach(input=>input.onchange=syncImportKind);
syncImportKind();
$('#import-form input[name="source"]').oninput=()=>document.querySelectorAll('.detected-source').forEach(button=>{button.classList.remove('active');button.querySelector('b').textContent='选择'});
const importStatusNames={new:'新游戏',append:'追加版本',missing:'ROM 缺失',duplicate:'已经存在',conflict:'需要处理'};
const importStatusHints={new:'将在资料库中添加游戏和版本',append:'将作为独立版本加入已有游戏',missing:'元数据存在，但对应 ROM 未找到；本次会跳过',duplicate:'相同条目已经收录，不会重复导入',conflict:'稳定 ID 与现有资料冲突，需要先手工处理'};
function setImportStep(step){document.querySelectorAll('#import-panel .flow-rail span').forEach((item,index)=>item.classList.toggle('active',index<step))}
function selectedImportTokens(){return [...document.querySelectorAll('#import-preview input:checked')].map(x=>x.value)}
function portablePlatformName(definition){return(locale.value==='en'||locale.value==='ja')?definition.name:(definition.name_zh||definition.name)}
function importCandidatePlatformName(candidate){const definition=candidate?.game?.platform_definition;if(definition)return portablePlatformName(definition);const platform=platformByValue(candidate?.game?.platform);return((locale.value==='en'||locale.value==='ja')?platform?.name:platform?.name_zh)||candidate?.game?.platform||''}
function refreshImportCandidatePlatformNames(){document.querySelectorAll('[data-import-index]').forEach(item=>{const candidate=lastImportCandidates[Number(item.dataset.importIndex)],label=item.querySelector('.preview-platform');if(candidate&&label)label.textContent=importCandidatePlatformName(candidate)})}
function selectedPortablePlatforms(){
	const selected=new Set(selectedImportTokens()),definitions=new Map();
	for(const candidate of lastImportCandidates){const definition=candidate.game?.platform_definition;if(definition&&selected.has(candidate.token)&&!definitions.has(definition.id))definitions.set(definition.id,definition)}
	return [...definitions.values()];
}
function renderPortablePlatformChanges(){
	const container=$('#import-platform-changes');if(!container)return;const definitions=selectedPortablePlatforms();container.hidden=definitions.length===0;if(!definitions.length){container.innerHTML='';return}
	container.innerHTML=`<header><span aria-hidden="true">${uiIcon('add')}</span><div><strong>${esc(tr('整合包携带自定义平台定义'))}</strong><small>${esc(tr('只创建所选条目缺少的定义，不覆盖本地设置。'))}</small></div><b>${definitions.length}</b></header><div>${definitions.map(definition=>{const existing=customPlatforms.find(item=>item.id===definition.id&&item.enabled),extensions=definition.extensions||[],category=platformCategoryNames[definition.category]||definition.category;return `<article><span><strong>${esc(portablePlatformName(definition))}</strong><code>${esc(definition.id)}</code></span><small>${esc(tr(category))} · ${esc(extensions.join(', ')||tr('未声明文件格式'))}</small><em>${esc(tr(existing?'复用本地相同定义':'将随所选条目创建'))}</em></article>`}).join('')}</div>`;
}
function renderImportSourceDiagnostics(diagnostics=[]){
	const container=$('#import-source-diagnostics');if(!container)return;const wrapped=diagnostics.find(item=>item.code==='wrapped_archives_detected')?.count||0,split=diagnostics.find(item=>item.code==='split_archives_detected')?.count||0,matchingWrapped=diagnostics.find(item=>item.code==='platform_wrapped_archives_detected')?.count||0,matchingSplit=diagnostics.find(item=>item.code==='platform_split_archive_parts_detected')?.count||0,matching=matchingWrapped+matchingSplit,limited=diagnostics.some(item=>item.code==='container_inspection_limited'),total=wrapped+split;
	container.hidden=total===0&&!limited;if(container.hidden){container.innerHTML='';return}
	const title=matching?tr('当前平台仍在封装中'):total?tr('ROM 内容目录可能不匹配'):tr('ROM 还没有准备好'),summary=matching?tr(`发现 ${matching} 个与当前平台匹配的封装文件。`):total?tr(`发现 ${total} 个封装文件，但未识别到当前平台。`):tr('容器检查只完成了安全范围内的一部分。'),guidance=matching?tr('请使用整合包提供的正规方式解包，再把 ROM 内容目录指向解包后的平台目录。服务不会自动解密或执行来源中的工具。'):tr('请确认 ROM 内容目录是否选择了当前平台；若文件仍在封装中，请用整合包提供的正规方式解包。服务不会自动解密或执行来源中的工具。');
	container.innerHTML=`<header><span aria-hidden="true">${uiIcon('warning')}</span><div><strong>${esc(title)}</strong><small>${esc(summary)}</small></div>${total?`<b>${matching||total}</b>`:''}</header><details><summary>${esc(tr('怎么处理'))}</summary><p>${esc(guidance)}</p>${limited?`<small>${esc(tr('目录较大，容器数量可能不完整。'))}</small>`:''}</details>`;
}
function renderImportSourceState(counts,candidates){
	const container=$('#import-source-state'),persisted=lastImportRequest?.kind==='persistent',importable=(counts.new||0)+(counts.append||0),waiting=persisted&&candidates.length>0&&importable===0&&(counts.missing||0)>0;
	container.hidden=!waiting;
	if(waiting){
		container.innerHTML=`<span aria-hidden="true">${uiIcon('rebuild')}</span><div><strong>${esc(tr('来源已保存，可稍后重扫'))}</strong><small>${esc(tr('当前没有可导入的 ROM。文件到位后，在上方“已保存来源”中重新扫描。'))}</small></div>`;
		$('#commit-summary').textContent=tr('等待 ROM 文件');$('#commit-note').textContent=tr('来源配置已保存，不会创建空游戏。');
	}else{
		container.innerHTML='';$('#commit-note').textContent=tr('重复和冲突项不会提交');
	}
}
function selectedPortableRuntime(){
	const selected=new Set(selectedImportTokens()),catalog={frontend_adapters:new Map(),device_profiles:new Map(),emulator_drivers:new Map(),retroarch_cores:new Map(),package_profile:null};
	for(const candidate of lastImportCandidates){if(!selected.has(candidate.token))continue;const runtime=candidate.game?.runtime_catalog;if(!runtime)continue;for(const key of ['frontend_adapters','device_profiles','emulator_drivers','retroarch_cores'])for(const item of runtime[key]||[])if(!catalog[key].has(item.id))catalog[key].set(item.id,item);if(runtime.package_profile)catalog.package_profile=runtime.package_profile}
	return catalog;
}
function renderPortableRuntimeChanges(){
	const container=$('#import-runtime-changes');if(!container)return;const catalog=selectedPortableRuntime(),groups=[['frontend_adapters',tr('前端适配器'),frontendAdapters],['device_profiles',tr('设备档案'),deviceProfiles],['emulator_drivers',tr('模拟器驱动'),emulatorDrivers],['retroarch_cores',tr('RetroArch 核心'),retroarchCores]],items=[];
	for(const [key,label,existingItems] of groups)for(const item of catalog[key].values())items.push({id:item.id,name:item.name||item.id,label,existing:existingItems.some(current=>current.id===item.id&&current.enabled)});if(catalog.package_profile)items.push({id:catalog.package_profile.id,name:catalog.package_profile.name||catalog.package_profile.id,label:tr('整合包模板'),existing:packageProfiles.some(current=>current.id===catalog.package_profile.id&&current.enabled)});
	container.hidden=items.length===0;if(!items.length){container.innerHTML='';return}container.innerHTML=`<header><span aria-hidden="true">${uiIcon('config')}</span><div><strong>${esc(tr('整合包携带可恢复的运行配置'))}</strong><small>${esc(tr('随所选 ROM 一并提交；启动建议仍需审核。'))}</small></div><b>${items.length}</b></header><div>${items.map(item=>`<article><span><strong>${esc(item.name)}</strong><code>${esc(item.id)}</code></span><small>${esc(item.label)}</small><em>${esc(tr(item.existing?'复用本地相同定义':'将随所选条目创建'))}</em></article>`).join('')}</div>`;
}
function updateImportSelection(){
	const selected=selectedImportTokens(),available=document.querySelectorAll('#import-preview input:not(:disabled)').length;
  $('#selection-count').textContent=`已选择 ${selected.length} / ${available} 项`;$('#commit-summary').textContent=selected.length?`将导入 ${selected.length} 个条目`:'尚未选择条目';
  const button=$('#commit-import');button.disabled=selected.length===0;button.querySelector('span').textContent=selected.length?`导入选择的 ${selected.length} 项`:'请选择条目';
  const all=$('#select-importable');all.checked=available>0&&selected.length===available;all.indeterminate=selected.length>0&&selected.length<available;
  renderPortablePlatformChanges();renderPortableRuntimeChanges();
  setImportStep(selected.length?3:2);
}
function renderImportPreview(r){
	const candidates=r.candidates||[],counts={new:0,append:0,missing:0,duplicate:0,conflict:0};candidates.forEach(c=>counts[c.status]=(counts[c.status]||0)+1);
	lastImportCandidates=candidates;
	$('#import-empty').hidden=true;$('#import-review').hidden=false;
	$('#import-summary').innerHTML=`<div class="summary-stat new"><strong>${(counts.new||0)+(counts.append||0)}</strong><small>可直接导入</small></div><div class="summary-stat pending"><strong>${counts.missing||0}</strong><small>缺失并跳过</small></div><div class="summary-stat duplicate"><strong>${counts.duplicate||0}</strong><small>已经存在</small></div><div class="summary-stat conflict"><strong>${counts.conflict||0}</strong><small>需要处理</small></div>`;
	renderImportSourceDiagnostics(r.source_diagnostics||[]);lastImportPreviewToken=r.preview_token||'';$('#import-preview').innerHTML=candidates.map((c,index)=>{const selectable=c.status==='new'||c.status==='append',artifacts=c.game.artifacts||[],media=c.game.media||[],platform=importCandidatePlatformName(c),missing=c.missing_artifacts||artifacts.filter(a=>a.missing).length,path=artifacts.map(a=>a.path).join(' · '),fileState=missing?`${missing} 个 ROM 缺失`:`${artifacts.length} 个 ROM 已确认`;return `<label class="preview-item" data-import-index="${index}"><input type="checkbox" value="${esc(c.token||'')}" ${selectable?'checked':'disabled'}><span class="preview-copy"><strong>${esc(c.game.edition_title||c.game.default_title||'名称缺失')}</strong><small>${esc(path||'未关联文件')}</small><em><span class="preview-platform">${esc(platform)}</span><b> · </b><span>${esc(fileState)}</span><b> · </b><span>${media.length} 项媒体</span><b> · </b><span>${esc(c.reason||importStatusHints[c.status]||'')}</span></em></span><span class="status-pill ${esc(c.status)}">${esc(importStatusNames[c.status]||c.status)}</span></label>`}).join('')||'<div class="review-empty"><h3>没有解析到条目</h3><p>请确认路径、平台与文件格式是否正确。</p></div>';
	document.querySelectorAll('#import-preview input').forEach(input=>input.onchange=updateImportSelection);$('#commit-import').hidden=!candidates.some(c=>c.status==='new'||c.status==='append');updateImportSelection();renderImportSourceState(counts,candidates);
}
$('#select-importable').onchange=e=>{document.querySelectorAll('#import-preview input:not(:disabled)').forEach(input=>input.checked=e.target.checked);updateImportSelection()};
$('#import-form').onsubmit=async e=>{
	e.preventDefault();const f=new FormData(e.target),button=e.submitter||$('#preview-import'),kind=f.get('import_kind');lastImportRequest=kind==='rom'?{kind,source:String(f.get('rom_source')).trim(),platform:String(f.get('platform')||'').trim(),rom_storage:f.get('rom_storage')}:{kind,format:f.get('format'),source:String(f.get('source')).trim(),content_root:String(f.get('content_root')||'').trim(),runtime_source:String(f.get('runtime_source')||'').trim(),platform:String(f.get('platform')||'').trim(),locale:f.get('locale')||'',rom_storage:f.get('rom_storage'),media_storage:f.get('media_storage')};
	button.disabled=true;button.classList.add('loading');button.querySelector('span').textContent=kind==='rom'?'正在扫描并计算指纹':'正在读取并比对';setInlineState('#import-inline-state',kind==='rom'?'正在扫描 ROM；预览完成前不会写入资料库…':'正在读取元数据并确认 ROM 是否存在；完成前不会写入资料库…','info');
	try{let r;if(f.get('remember_source')){const body=persistentSourceBody(lastImportRequest,f);let source=librarySources.find(item=>samePersistentSource(item,body));if(source){source=await api(`/api/sources/${encodeURIComponent(source.id)}`,{method:'PUT',body:JSON.stringify(body)})}else{source=await api('/api/sources',{method:'POST',body:JSON.stringify(body)})}r=await api(`/api/sources/${encodeURIComponent(source.id)}/scans`,{method:'POST',body:'{}'});lastSourceScanID=r.scan.id;lastImportRequest={kind:'persistent',source_id:source.id};await loadLibrarySources()}else{lastSourceScanID='';const endpoint=kind==='rom'?'/api/imports/roms/preview':'/api/imports/preview',requestBody={...lastImportRequest};delete requestBody.kind;r=await api(endpoint,{method:'POST',body:JSON.stringify(requestBody)})}renderImportPreview(r);setInlineState('#import-inline-state','');toast(`已检查 ${r.scan?.candidate_count??r.parsed} 个条目，请审查变化`)}catch(err){setInlineState('#import-inline-state',err.message,'error');toast(err.message,true)}finally{button.disabled=false;button.classList.remove('loading');button.querySelector('span').textContent=kind==='rom'?'扫描 ROM 并生成预览':'检查元数据并生成预览'}
};
$('#commit-import').onclick=async()=>{
	if(!lastImportRequest)return;const selectedTokens=selectedImportTokens();if(!selectedTokens.length){toast('请至少选择一个条目',true);return}const button=$('#commit-import');
  button.disabled=true;button.classList.add('loading');button.querySelector('span').textContent='正在写入资料库';
	try{const persistent=lastImportRequest.kind==='persistent',endpoint=persistent?`/api/source-scans/${encodeURIComponent(lastSourceScanID)}/commit`:lastImportRequest.kind==='rom'?'/api/imports/roms/commit':'/api/imports/commit',requestBody=persistent?{preview_token:lastImportPreviewToken,selected_tokens:selectedTokens}:{...lastImportRequest,preview_token:lastImportPreviewToken,selected_tokens:selectedTokens};delete requestBody.kind;const r=await api(endpoint,{method:'POST',body:JSON.stringify(requestBody)}),imported=r.Imported??r.imported??0,skipped=r.Skipped??r.skipped??0;document.querySelectorAll('#import-preview input').forEach(input=>input.disabled=true);lastImportCandidates=[];renderPortablePlatformChanges();renderPortableRuntimeChanges();button.querySelector('span').textContent=tr(`已导入 ${imported} 项`);$('#commit-summary').textContent=tr('导入完成');setImportStep(3);setInlineState('#import-inline-state',tr(`成功导入 ${imported} 项，跳过 ${skipped} 项；复制 ${r.rom_files_copied||0} 个 ROM 文件、${r.media_files_copied||0} 个媒体文件。`),'info');toast(tr(`已导入 ${imported} 条，跳过 ${skipped} 条`));await Promise.all([load(),loadLibrarySources(),loadPlatformPresets()])}catch(err){button.disabled=false;button.querySelector('span').textContent=tr('重新提交');setInlineState('#import-inline-state',err.message,'error');toast(err.message,true)}finally{button.classList.remove('loading')}
};

async function loadDevices(){try{devices=await api('/api/devices');renderSyncStatus()}catch(e){toast(e.message,true)}}
async function loadSaveRevisions(){try{saveRevisions=await api('/api/saves');renderSyncStatus();renderSaveHistory();render()}catch(e){toast(e.message,true)}}
async function loadSyncSessions(){try{syncSessions=await api('/api/sync/sessions');renderSyncSessions();renderSyncOverview()}catch(e){toast(e.message,true)}}
async function loadInventoryMatchReviews(){try{inventoryMatchReviews=await api(`/api/sync/inventory-matches?locale=${encodeURIComponent(locale.value)}`);const active=new Set(inventoryMatchReviews.map(item=>item.inventory_item_id));for(const key of inventoryMatchSelections.keys())if(!active.has(key))inventoryMatchSelections.delete(key);for(const key of inventoryMatchPreviews.keys())if(!active.has(key))inventoryMatchPreviews.delete(key);renderInventoryMatchReviews()}catch(e){toast(e.message,true)}}
function inventoryMatchMethodName(method){return tr(({serial:'序列号',product_code:'产品码',title_id:'Title ID'})[method]||method)}
function syncPlatformName(value){const definition=platformByValue(value);return((locale.value==='en'||locale.value==='ja')?definition?.name:definition?.name_zh)||definition?.name||value}
function syncDriverName(value){return String(value||tr('客户端')).replace(/^builtin-driver-/,'').replace(/^custom-driver-/,'')}
function renderInventoryMatchReviews(){
  const list=$('#inventory-match-list'),count=$('#inventory-match-count');if(!list||!count)return;count.textContent=tr(`${inventoryMatchReviews.length} 项`);list.closest('.sync-panel')?.classList.toggle('is-empty',!inventoryMatchReviews.length);
  list.innerHTML=inventoryMatchReviews.map(review=>{const key=review.inventory_item_id,selected=inventoryMatchSelections.get(key)||'',preview=inventoryMatchPreviews.get(key),selectedCandidate=review.candidates.find(candidate=>candidate.edition_id===(preview?.editionId||selected));return `<section class="inventory-match-card" data-inventory-review="${esc(key)}"><div class="inventory-match-meta"><span class="match-device">${esc(review.device_name)}</span><span>${esc(syncPlatformName(review.platform_id))}</span><span>${esc(inventoryMatchMethodName(review.match_method))}</span><span>${esc(compactBytes(review.size))}</span></div><div class="inventory-candidates" role="radiogroup" aria-label="${esc(tr('候选游戏版本'))}">${review.candidates.map((candidate,index)=>`<label class="inventory-candidate ${selected===candidate.edition_id?'selected':''}"><input type="radio" name="match-${esc(key)}" value="${esc(candidate.edition_id)}" ${selected===candidate.edition_id?'checked':''}><span class="candidate-index">${String(index+1).padStart(2,'0')}</span><span class="candidate-copy"><strong>${esc(candidate.game_title)}</strong><small>${esc(candidate.edition_title)} · ${esc(tr(typeName[candidate.edition_type]||candidate.edition_type))}</small></span><span class="candidate-platform">${esc(syncPlatformName(candidate.platform_id))}</span></label>`).join('')}</div><div class="inventory-match-action">${preview&&selectedCandidate?`<div class="match-confirmation"><strong>${esc(tr('确认关联'))}：${esc(selectedCandidate.game_title)} · ${esc(selectedCandidate.edition_title)}</strong><small>${esc(tr('不会更改当前 ROM；下一次同步开始生效。ROM 身份或候选版本变化时会拒绝提交。'))}</small></div><button type="button" class="primary" data-inventory-commit>${esc(tr('确认并保存'))}</button>`:`<div class="match-boundary"><strong>${esc(tr('先选择正确的游戏版本'))}</strong><small>${esc(tr('确认仅作用于这台设备上的同一 ROM 身份，不会合并游戏或存档。'))}</small></div><button type="button" data-inventory-preview ${selected?'':'disabled'}>${esc(tr('预览确认'))}</button>`}</div></section>`}).join('')||`<div class="sync-empty"><strong>${esc(tr('没有需要确认的 ROM'))}</strong><small>${esc(tr('唯一指纹会自动关联；无法匹配的 ROM 保持未关联，不会被猜测。'))}</small></div>`;
  list.querySelectorAll('input[type="radio"]').forEach(input=>input.onchange=()=>{const card=input.closest('[data-inventory-review]'),key=card.dataset.inventoryReview;inventoryMatchSelections.set(key,input.value);inventoryMatchPreviews.delete(key);renderInventoryMatchReviews()});
  list.querySelectorAll('[data-inventory-preview]').forEach(button=>button.onclick=()=>previewInventoryMatch(button.closest('[data-inventory-review]').dataset.inventoryReview,button));
  list.querySelectorAll('[data-inventory-commit]').forEach(button=>button.onclick=()=>commitInventoryMatch(button.closest('[data-inventory-review]').dataset.inventoryReview,button));
}
async function previewInventoryMatch(key,button){const review=inventoryMatchReviews.find(item=>item.inventory_item_id===key),editionID=inventoryMatchSelections.get(key);if(!review||!editionID)return;button.disabled=true;try{const response=await api(`/api/sync/inventory-matches/preview?locale=${encodeURIComponent(locale.value)}`,{method:'POST',body:JSON.stringify({session_id:review.session_id,inventory_item_id:key,edition_id:editionID})});inventoryMatchPreviews.set(key,{token:response.preview_token,editionId:response.selected_edition_id});renderInventoryMatchReviews()}catch(err){inventoryMatchPreviews.delete(key);toast(err.message,true);button.disabled=false}}
async function commitInventoryMatch(key,button){const review=inventoryMatchReviews.find(item=>item.inventory_item_id===key),preview=inventoryMatchPreviews.get(key);if(!review||!preview)return;button.disabled=true;try{await api('/api/sync/inventory-matches/commit',{method:'POST',body:JSON.stringify({session_id:review.session_id,inventory_item_id:key,edition_id:preview.editionId,preview_token:preview.token})});inventoryMatchSelections.delete(key);inventoryMatchPreviews.delete(key);toast(tr('ROM 版本关联已确认，将用于下一次同步'));await Promise.all([loadInventoryMatchReviews(),loadSyncSessions()])}catch(err){inventoryMatchPreviews.delete(key);renderInventoryMatchReviews();toast(err.message,true)}}
function renderDevices(){
  if(!$('#device-list'))return;$('#device-count-label').textContent=`${devices.length} 台`;$('#device-list').closest('.sync-panel')?.classList.toggle('is-empty',!devices.length&&!syncSessions.length);
  $('#device-list').innerHTML=devices.map(d=>{const seen=d.last_seen_at?new Date(d.last_seen_at):null,recent=d.status!=='revoked'&&seen&&Date.now()-seen.getTime()<7*86400000,seenLabel=seen?tr(`最近连接 ${shortDate(seen)}`):tr('尚未连接'),runtime=d.capabilities?.verified_save_bridge?tr('已核验兼容核心'):d.capabilities?.runtime_probe?d.capabilities?.emulator_installed?(d.capabilities?.retroarch_core_installed?tr('已发现模拟器与核心'):tr('已发现模拟器')):tr('尚未发现模拟器'):tr('等待运行环境探测');return `<div class="device-row"><span class="device-status ${d.status==='revoked'?'revoked':recent?'online':'idle'}"><i></i></span><span><strong>${esc(d.name)}</strong><small>${esc(d.distribution||d.os_family)} · ${esc(d.architecture||'unknown')} · ${esc(seenLabel)} · ${esc(runtime)}</small></span><span class="device-badge">${esc(tr(deviceStatusName[d.status]||d.status||d.os_family))}</span></div>`}).join('')||`<div class="sync-empty"><strong>${esc(tr('还没有已配对客户端'))}</strong><small>${esc(tr('生成配对码，在设备客户端完成连接。'))}</small></div>`
}
function renderSyncSessions(){if(!$('#sync-session-list'))return;const names=new Map(devices.map(d=>[d.id,d.name]));$('#sync-session-list').closest('.sync-panel')?.classList.toggle('is-empty',!devices.length&&!syncSessions.length);$('#sync-session-list').innerHTML=syncSessions.slice(0,6).map(session=>`<div class="sync-session-row ${esc(session.status)} ${session.conflict_count?'conflict':''}"><strong>${esc(names.get(session.device_id)||session.device_id.slice(0,8))}</strong><small>${shortDate(session.updated_at)} · <span class="sync-transfer" aria-label="${esc(tr(`上传 ${session.uploaded_count}`))}">${uiIcon('upload')}${session.uploaded_count}</span> <span class="sync-transfer" aria-label="${esc(tr(`下载 ${session.downloaded_count}`))}">${uiIcon('download')}${session.downloaded_count}</span>${session.conflict_count?` · ${esc(tr(`${session.conflict_count} 个冲突`))}`:''}</small><em>${esc(tr(syncStatusName[session.status]||session.status))}</em></div>`).join('')||'<div class="sync-empty"><small>尚无同步会话</small></div>'}
function renderSyncOverview(){
  if(!$('#sync-overview'))return;const editions=allGames.flatMap(w=>w.editions),protectedEditions=new Set([...saveBindings.map(binding=>binding.edition_id),...saveRevisions.map(revision=>revision.edition_id).filter(Boolean)]).size,conflicts=saveRevisions.filter(r=>r.status==='conflict'||r.conflict).length;
  $('#sync-overview').innerHTML=`<div><span>已配对设备</span><strong>${devices.length}</strong><small>由客户端自动登记</small></div><div><span>已关联版本</span><strong>${protectedEditions} / ${editions.length}</strong><small>存档绑定到独立版本</small></div><div><span>历史快照</span><strong>${saveRevisions.length}</strong><small>内容不可覆盖</small></div><div class="${conflicts?'has-conflict':''}"><span>待处理冲突</span><strong>${conflicts}</strong><small>${conflicts?'双方版本均已保留':'当前同步一致'}</small></div>`
}
function renderSyncCoverage(){
  if(!$('#sync-coverage'))return;const editions=allGames.flatMap(game=>game.editions.map(edition=>({game,edition})));$('#sync-coverage').closest('.sync-panel')?.classList.toggle('is-empty',!editions.length);
  const rows=editions.map(({game,edition})=>{const summary=editionSaveSummary(edition),files=editionArtifactSummary(edition),hashed=files.usable||0,romState=!files.total?'未关联 ROM':files.missing?'ROM 文件缺失':`${hashed} / ${files.total} 个指纹`,editionSubtitle=game.display_title===edition.display_title?'':`<small>${esc(edition.display_title)}</small>`,syncMeta=summary.latest?`<small>${esc(syncDriverName(summary.latest.driver_id))} · ${shortDate(summary.latest.created_at)}</small>`:'';return `<div class="coverage-row"><span class="coverage-title"><strong>${esc(game.display_title)}</strong>${editionSubtitle}</span><span><strong>${esc(syncPlatformName(game.platform))}</strong><small>${esc(tr(typeName[edition.edition_type]||edition.edition_type))}</small></span><span class="coverage-identity ${hashed?'ready':'pending'}"><strong>${esc(romState)}</strong></span><span class="coverage-save ${summary.conflicts?'conflict':''}"><strong>${summary.count?`${summary.count} 个快照`:'等待首次同步'}</strong>${syncMeta}</span></div>`}).join('');
  $('#sync-coverage').innerHTML=rows?`<div class="coverage-head" aria-hidden="true"><span>${esc(tr('游戏版本'))}</span><span>${esc(tr('平台'))}</span><span>${esc(tr('ROM 识别'))}</span><span>${esc(tr('同步状态'))}</span></div>${rows}`:'<div class="sync-empty"><strong>还没有可同步的游戏版本</strong><small>先导入带真实 ROM 文件的游戏，客户端才能用指纹建立关联。</small></div>'
}
function renderSyncStatus(){renderDevices();renderSyncSessions();renderSyncOverview();renderSyncCoverage()}
function revisionTitle(r){const stream=saveStreams.find(item=>item.id===r.stream_id),editionID=r.edition_id||stream?.editions?.[0]?.edition_id||'',found=findEdition(editionID);if(!found)return stream?.namespace||tr('共享存档流');return found.game.display_title===found.edition.display_title?found.game.display_title:`${found.game.display_title} · ${found.edition.display_title}`}
function renderSaveHistory(){if(!$('#save-history'))return;const deviceMap=new Map(devices.map(d=>[d.id,d.name]));$('#save-history').closest('.sync-panel')?.classList.toggle('is-empty',!saveRevisions.length);$('#save-history').innerHTML=saveRevisions.map(r=>{const files=r.files||[],archive=files.length>1?`<button class="download-link" data-revision-archive="${esc(r.id)}">${tr('下载完整快照')}</button>`:'',links=files.map(file=>`<button class="download-link" data-revision-download="${esc(r.id)}" data-file-download="${esc(file.id)}" data-file-name="${esc(file.logical_path)}">${files.length>1?esc(file.logical_path):tr('下载历史版本')}</button>`).join(''),fileCount=tr(`${r.file_count||files.length} 个文件`);return `<div class="history-row"><span class="history-meta"><strong>${esc(revisionTitle(r))}</strong><small>${esc(deviceMap.get(r.device_id)||r.device_id.slice(0,8))} · ${esc(syncDriverName(r.driver_id))} · ${esc(fileCount)} · ${compactBytes(r.total_size??r.size??0)} · ${new Date(r.created_at).toLocaleString()}</small></span><span class="history-actions">${r.status==='conflict'||r.conflict?`<span class="conflict-badge">${tr('冲突保留')}</span>`:''}${archive}${links}</span></div>`}).join('')||'<div class="sync-empty"><strong>尚无存档历史</strong><small>客户端完成首次同步后，这里会出现可恢复的版本。</small></div>';document.querySelectorAll('[data-revision-download]').forEach(button=>button.onclick=()=>downloadSaveFile(button));document.querySelectorAll('[data-revision-archive]').forEach(button=>button.onclick=()=>downloadSaveArchive(button))}
async function downloadSaveFile(button){button.disabled=true;try{const token=sessionStorage.getItem('game-library-token'),headers=token?{Authorization:`Bearer ${token}`}:{},response=await fetch(`/api/v1/save-revisions/${encodeURIComponent(button.dataset.revisionDownload)}/files/${encodeURIComponent(button.dataset.fileDownload)}/content`,{headers});if(!response.ok)throw new Error(`HTTP ${response.status}`);const url=URL.createObjectURL(await response.blob()),link=document.createElement('a');link.href=url;link.download=button.dataset.fileName.split('/').pop()||'save.bin';link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(err){toast(err.message,true)}finally{button.disabled=false}}
async function downloadSaveArchive(button){button.disabled=true;try{const token=sessionStorage.getItem('game-library-token'),headers=token?{Authorization:`Bearer ${token}`}:{},id=button.dataset.revisionArchive,response=await fetch(`/api/v1/save-revisions/${encodeURIComponent(id)}/archive`,{headers});if(!response.ok)throw new Error(`HTTP ${response.status}`);const url=URL.createObjectURL(await response.blob()),link=document.createElement('a');link.href=url;link.download=`save-snapshot-${id.slice(0,8)}.zip`;link.click();setTimeout(()=>URL.revokeObjectURL(url),1000)}catch(err){toast(err.message,true)}finally{button.disabled=false}}

function renderPairingCodeResult(){
	const result=$('#pairing-code-result');if(!result||!pairingCodeView)return;
	const {code,allowHTTP,rootExample,target}=pairingCodeView,android=target==='android',command=`varkiv agent pair --server ${location.origin} --code ${code} --name "${tr('我的掌机')}" --root "${rootExample}"${allowHTTP}`,detail=android?location.origin:command,copyValue=android?code:command;
	result.hidden=false;result.innerHTML=`<strong>${esc(code)}</strong><span><small>${esc(tr(android?'在 Android 客户端填写服务地址和上方配对码；令牌只返回一次':'10 分钟内在设备上完成配对；令牌只返回一次'))}</small><code>${esc(detail)}</code></span><button type="button">${esc(tr(android?'复制配对码':'复制命令'))}</button>`;
	result.querySelector('button').onclick=async()=>{await navigator.clipboard.writeText(copyValue);toast(tr(android?'配对码已复制':'配对命令已复制'))};
}
$('#issue-pairing-code').onclick=async()=>{const button=$('#issue-pairing-code'),profile=$('#pair-device-profile').value;if(!profile){toast(tr('请先选择设备预设'),true);return}button.disabled=true;try{const response=await api('/api/pairing-codes',{method:'POST',body:JSON.stringify({expires_in_seconds:600,requested_device:{device_profile_id:profile}})}),allowHTTP=location.protocol==='http:'&&!['localhost','127.0.0.1','::1'].includes(location.hostname)?' --allow-http':'',device=deviceProfiles.find(item=>item.id===profile),rootExample=device?.os_family==='windows'?'C:\\Varkiv':'$HOME/.local/share/varkiv';pairingCodeView={code:response.code,allowHTTP,rootExample,target:device?.target||''};renderPairingCodeResult()}catch(err){toast(err.message,true)}finally{button.disabled=false}}

$('#new-game').onclick=()=>libraryMode==='series'?openNewSeries():openNewGame();$('.empty-new').onclick=openNewGame;document.querySelectorAll('[data-close]').forEach(b=>b.onclick=()=>b.closest('dialog').close());locale.onchange=async()=>{refreshPlatformOptions();renderPlatformCatalog();renderProfileGrid();renderProfile();renderImportSources();renderLibrarySources();renderHashSources();renderHashPackPreview();renderRuntimeCatalog();renderSupportReadiness();renderWebEmulatorReadiness();renderAcceptanceReportSummary();refreshHardwareAcceptanceChoices();renderHardwareAcceptancePreview();renderManagedCleanupRuns();renderManagedCleanupPreview();renderPairingProfiles();renderPairingCodeResult();renderPortablePlatformChanges();renderPortableRuntimeChanges();refreshImportCandidatePlatformNames();renderRuntimeHintBatchPreview();await Promise.all([load(),loadInventoryMatchReviews()])};search.oninput=render;
document.querySelectorAll('[data-close-player]').forEach(button=>button.onclick=closeWebPlayer);webPlayerDialog.addEventListener('close',()=>{$('#web-player-frame').src='about:blank';setWebPlayerState('idle');setWebPlayerInput({supported:true,count:0})});
document.querySelectorAll('[data-close-netplay]').forEach(button=>button.onclick=()=>webNetplayDialog.close());document.querySelectorAll('#web-netplay-form input[name="mode"]').forEach(input=>input.onchange=syncWebNetplayMode);$('#web-netplay-form').onsubmit=submitWebNetplay;$('#copy-web-player-invite').onclick=copyWebNetplayInvite;
window.addEventListener('message',event=>{const frame=$('#web-player-frame'),message=event.data;if(event.origin!==location.origin||event.source!==frame?.contentWindow)return;if(message?.type==='varkiv:web-player-state'&&Object.hasOwn(webPlayerStateName,message.state))setWebPlayerState(message.state);if(message?.type==='varkiv:web-player-input'&&typeof message.supported==='boolean'&&Number.isInteger(message.count)&&Number.isInteger(message.standard))setWebPlayerInput(message);if(message?.type==='varkiv:web-netplay-state'&&Object.hasOwn(webNetplayStateName,message.state)&&Number.isInteger(message.players))setWebNetplayState(message.state,message.players)});
bindMediaPicker('game-media-file','game-media-file-name');bindMediaPicker('media-file','media-file-name');
$('#hash-pack-file').onchange=()=>{hashPackFile=$('#hash-pack-file').files?.[0]||null;$('#hash-pack-file-name').textContent=hashPackFile?.name||tr('尚未选择文件');resetHashPackPreview()};
$('#preview-hash-pack').onclick=previewHashPack;$('#hash-pack-export-form').onsubmit=exportHashPack;
document.querySelectorAll('.recheck-media').forEach(button=>button.onclick=()=>recheckMediaContent(button));
document.addEventListener('keydown',e=>{if(e.key==='/'&&!['INPUT','SELECT','TEXTAREA'].includes(document.activeElement.tagName)){e.preventDefault();search.focus()}if(e.key==='Escape'&&document.activeElement===search){search.value='';search.blur();render()}});
async function loadCapabilities(){capabilities=await api('/api/capabilities')}
async function loadApplication(){await Promise.all([loadCapabilities(),loadWebEmulatorReadiness(),loadWebNetplayReadiness(),load(),loadPlatformPresets(),loadRuntimeCatalog(),loadSupportReadiness(),loadPackageProfiles(),loadConfigTemplatePresets(),loadImportSources(),loadLibrarySources(),loadHashSources(),loadDevices(),loadSaveRevisions(),loadSyncSessions(),loadInventoryMatchReviews(),loadManagedCleanupRuns()])}
$('#auth-form').onsubmit=async e=>{e.preventDefault();const token=new FormData(e.target).get('token');sessionStorage.setItem('game-library-token',token);const button=e.submitter;button.disabled=true;try{devices=await api('/api/devices');$('#auth-dialog').close();renderSyncStatus();await Promise.all([loadWebEmulatorReadiness(),loadWebNetplayReadiness(),load(),loadRuntimeCatalog(),loadPackageProfiles(),loadConfigTemplatePresets(),loadSaveRevisions(),loadInventoryMatchReviews()])}catch(err){toast(err.message,true)}finally{button.disabled=false}};
function viewFromHash(){
  const hash=location.hash;
  if(hash==='#sources'||hash==='#transfer')return'sources-view';
  if(hash==='#packages')return'packages-view';
  if(hash==='#sync'||hash==='#devices')return'devices-view';
  if(hash==='#settings')return'settings-view';
  if(hash==='#platforms')return'platforms-view';
  return'library-view';
}
window.addEventListener('hashchange',()=>switchView(viewFromHash()));
async function bootstrap(){switchView(viewFromHash());try{const health=await fetch('/api/v1/health').then(r=>r.json());if(health.auth_required&&!sessionStorage.getItem('game-library-token')){showAuth();return}await loadApplication()}catch(e){toast(e.message,true)}}
bootstrap();
