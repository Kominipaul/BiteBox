(function(){
const NS="http://www.w3.org/2000/svg";
const $=id=>document.getElementById(id);
const pad=n=>String(n).padStart(2,"0");
const money=new Intl.NumberFormat("en-IE",{style:"currency",currency:"EUR",maximumFractionDigits:0});
const esc=EgoFloor.esc;
const isDesk=()=>window.innerWidth>=900;
const {data:DATA,phone:PHONE}=JSON.parse(document.getElementById("pageData").textContent);

/* ---------- Venue layout: real tables from the server ---------- */
const WP=EgoFloor.WP;
const spots=EgoFloor.place(DATA.tables).filter(s=>s.placed).map(s=>Object.assign(s,{dur:s.dur||DATA.duration}));
const HP=EgoFloor.HP;
const byId=Object.fromEntries(spots.map(s=>[s.id,s]));
const zones=[...new Set(spots.map(s=>s.zone))];

/* Orientation: portrait on phones, rotated a quarter turn (sea on the left) on wide screens */
let land=false;
const Wn=()=>land?HP:WP, Hn=()=>land?WP:HP;
const f1=n=>+n.toFixed(1);

const noun=s=>({set:"sunbed set",cabana:"cabana",lounge:"lounge"})[s.kind]||"table";
const nameOf=s=>({set:`Sunbeds ${s.label}`,cabana:`Cabana ${s.label}`,lounge:`Terrace lounge ${s.label}`,high:`Garden table ${s.label}`,rect4:`Pergola table ${s.label}`})[s.kind]||`Table ${s.label}`;
const seatsText=s=>s.min===s.max?`${s.max}`:`${s.min} to ${s.max}`;
const depositOf=s=>s.fee||0;
const feeName=s=>s.kind==="set"?"Booking fee":"Deposit";
// Full-day spots (beach sets, cabanas) are booked for their whole day window.
const slotText=(s,t,end)=>s.fullDay?`Full day, ${s.from||"10:00"} to ${s.until||"19:00"}`:`${t} to ${end||endTime(t,s.dur)}`;

/* ---------- State ---------- */
const dates=DATA.dates;
const state={date:dates[0].iso,time:null,guests:2,sel:null,hold:null,step:"browse",view:"map",notice:null,booking:null,avail:{times:[],taken:{}}};
const times=()=>state.avail.times;

/* ---------- Availability from the server ---------- */
const hourOf=t=>+t.slice(0,2);
const dateObj=iso=>dates.find(d=>d.iso===iso);
function ruleReason(s,time){
  if(!s.bookable)return `${nameOf(s)} can't be booked online.${PHONE?` Call us on ${PHONE}.`:""}`;
  if(!s.fullDay&&s.until&&time>s.until)return `${nameOf(s)} can be booked until ${s.until}.`;
  if(!s.fullDay&&s.from&&time<s.from)return s.kind==="lounge"?`Terrace lounges open from ${s.from}.`:`${nameOf(s)} can be booked from ${s.from}.`;
  if(state.guests<s.min)return `${nameOf(s)} is for groups of ${s.min} or more.`;
  if(state.guests>s.max)return `${nameOf(s)} seats up to ${s.max}.`;
  return null;
}
const booked=(s,time)=>(state.avail.taken[time]||[]).includes(s.n);
const isAvail=(s,time=state.time)=>!!time&&!ruleReason(s,time)&&!booked(s,time);
function statusOf(s){
  if(state.hold&&state.hold.id===s.id)return "selected";
  if(!state.hold&&state.sel===s.id&&isAvail(s))return "selected";
  return isAvail(s)?"available":"unavailable";
}
const availCount=(time=state.time)=>spots.filter(s=>isAvail(s,time)).length;

// live=true: a background refresh — flash spots other guests just took.
async function loadAvail(live){
  const before=state.time?(state.avail.taken[state.time]||[]).slice():[];
  try{const r=await fetch(`/book/availability?date=${state.date}`,{cache:"no-store"});state.avail=await r.json();}
  catch(_){if(!live)state.avail={times:[],taken:{}};return;}
  if(!times().includes(state.time))state.time=times().find(t=>t>="20:00")||times()[times().length-1]||null;
  if(live){
    const now=state.avail.taken[state.time]||[];
    now.filter(n=>!before.includes(n)).forEach(n=>{const s=byId[String(n)];if(!s||(state.hold&&state.hold.id===s.id))return;if(state.sel===s.id&&!state.hold){conflict(s);return;}flash(s);});
  }else if(state.sel&&!isAvail(byId[state.sel]))state.sel=null;
  render();
}

/* ---------- Drawing ---------- */
function drawMap(){
  $("map").innerHTML=EgoFloor.background(land,HP,"More spots")+`<g id="spots">${spots.map(s=>EgoFloor.spotSVG(s,land)).join("")}</g>`;
}
function paintSpots(){
  document.querySelectorAll("#spots .spot").forEach(g=>{
    const s=byId[g.dataset.id],st=statusOf(s);
    g.setAttribute("class","spot "+st);
    g.setAttribute("aria-pressed",st==="selected"?"true":"false");
    g.setAttribute("aria-disabled",st==="unavailable"?"true":"false");
    g.setAttribute("aria-label",`${nameOf(s)}, ${seatsText(s)} guests, ${st==="selected"?"your pick":st}`);
  });
}
function flash(s){
  const g=document.querySelector(`#spots .spot[data-id="${s.id}"]`);if(!g)return;
  const c=document.createElementNS(NS,"circle");c.setAttribute("class","ring");c.setAttribute("r",Math.max(s.w,s.h)/2+6);
  g.appendChild(c);setTimeout(()=>c.remove(),1300);
}

/* ---------- Full-bleed zoom & pan ---------- */
const svg=$("map"),stage=$("stage");
let vb={x:0,y:0,w:WP,h:HP},maxW=WP*2,userMoved=false,drawn=false;
function insets(){
  if(isDesk())return {r:parseFloat(getComputedStyle(document.documentElement).getPropertyValue("--panelW"))+32,b:0,t:0};
  const p=$("panel");return {r:0,b:p?Math.min(p.offsetHeight,stage.clientHeight*.45):0,t:54};
}
function applyVB(){
  const cw=stage.clientWidth||1,ch=stage.clientHeight||1,W=Wn(),H=Hn();
  vb.w=Math.min(maxW,Math.max(220,vb.w));vb.h=vb.w*ch/cw;
  vb.x=Math.min(W-vb.w*.3,Math.max(-vb.w*.7,vb.x));
  vb.y=Math.min(H-vb.h*.3,Math.max(-vb.h*.7,vb.y));
  svg.setAttribute("viewBox",`${f1(vb.x)} ${f1(vb.y)} ${f1(vb.w)} ${f1(vb.h)}`);
}
function wantLand(){
  const cw=stage.clientWidth,ch=stage.clientHeight,ins=insets();
  return (cw-ins.r)/Math.max(1,ch-ins.b)>1.1;
}
function fit(){
  const cw=stage.clientWidth,ch=stage.clientHeight;if(!cw||!ch)return;
  const l=wantLand();if(l!==land||!drawn){land=l;drawn=true;drawMap();paintSpots();}
  const W=Wn(),H=Hn(),ins=insets();
  const aw=Math.max(200,cw-ins.r),ah=Math.max(200,ch-ins.b-ins.t);
  let scale=Math.min(aw/(W*1.03),ah/(H*1.03));
  const phone=!isDesk()&&!land;
  if(phone)scale=aw/(W*1.03);                       // fill the width on phones, scroll the rest
  vb.w=cw/scale;vb.h=ch/scale;
  vb.x=W/2-(aw/2)/scale;
  vb.y=(phone&&H*scale>ah)?-ins.t/scale:H/2-(ins.t+ah/2)/scale;
  maxW=Math.max(vb.w*1.5,W*1.15);userMoved=false;applyVB();
}
function toSvg(cx,cy){const r=svg.getBoundingClientRect();return {x:vb.x+(cx-r.left)/r.width*vb.w,y:vb.y+(cy-r.top)/r.height*vb.h};}
function zoomAt(cx,cy,f){const p=toSvg(cx,cy);const nw=Math.min(maxW,Math.max(220,vb.w*f));const k=nw/vb.w;vb.x=p.x-(p.x-vb.x)*k;vb.y=p.y-(p.y-vb.y)*k;vb.w=nw;userMoved=true;applyVB();}
function zoomCenter(f){const r=svg.getBoundingClientRect();const ins=insets();zoomAt(r.left+(r.width-ins.r)/2,r.top+(r.height-ins.b)/2,f);}
new ResizeObserver(()=>{
  if(!userMoved||wantLand()!==land)fit();
  else{const cx=vb.x+vb.w/2,cy=vb.y+vb.h/2;vb.h=vb.w*stage.clientHeight/stage.clientWidth;vb.x=cx-vb.w/2;vb.y=cy-vb.h/2;applyVB();}
}).observe(stage);

const ptrs=new Map();let down=null,pinch=null;
svg.addEventListener("pointerdown",e=>{
  ptrs.set(e.pointerId,{x:e.clientX,y:e.clientY});
  try{svg.setPointerCapture(e.pointerId);}catch(_){}
  if(ptrs.size===1){const g=e.target.closest(".spot");down={x:e.clientX,y:e.clientY,id:g?g.dataset.id:null,moved:false};}
  else{if(down)down.moved=true;pinch=null;}
});
svg.addEventListener("pointermove",e=>{
  if(!ptrs.has(e.pointerId))return;
  const prev=ptrs.get(e.pointerId),cur={x:e.clientX,y:e.clientY};ptrs.set(e.pointerId,cur);
  const r=svg.getBoundingClientRect();
  if(ptrs.size===1&&down){
    if(!down.moved&&Math.hypot(cur.x-down.x,cur.y-down.y)>6){down.moved=true;svg.classList.add("dragging");}
    if(down.moved){vb.x-=(cur.x-prev.x)/r.width*vb.w;vb.y-=(cur.y-prev.y)/r.height*vb.h;userMoved=true;applyVB();}
  }else if(ptrs.size===2){
    const [a,b]=[...ptrs.values()];const d=Math.hypot(a.x-b.x,a.y-b.y);const m={x:(a.x+b.x)/2,y:(a.y+b.y)/2};
    if(pinch){vb.x-=(m.x-pinch.m.x)/r.width*vb.w;vb.y-=(m.y-pinch.m.y)/r.height*vb.h;applyVB();if(d>0)zoomAt(m.x,m.y,pinch.d/d);}
    pinch={d,m};
  }
});
function endPtr(e){
  if(!ptrs.has(e.pointerId))return;ptrs.delete(e.pointerId);
  if(ptrs.size===0){svg.classList.remove("dragging");if(down&&!down.moved&&e.type==="pointerup"){if(down.id)onSpot(down.id);else dismissPanel();}down=null;pinch=null;}
  else pinch=null;
}
svg.addEventListener("pointerup",endPtr);svg.addEventListener("pointercancel",endPtr);
svg.addEventListener("wheel",e=>{e.preventDefault();zoomAt(e.clientX,e.clientY,Math.exp(e.deltaY*(e.ctrlKey?.01:.0015)));},{passive:false});
svg.addEventListener("keydown",e=>{if(e.key!=="Enter"&&e.key!==" ")return;const g=e.target.closest&&e.target.closest(".spot");if(!g)return;e.preventDefault();onSpot(g.dataset.id);});
$("zIn").onclick=()=>zoomCenter(1/1.35);$("zOut").onclick=()=>zoomCenter(1.35);$("zFit").onclick=fit;

/* ---------- Controls ---------- */
function renderControls(){
  const locked=!!state.hold||state.step==="done";
  $("dates").innerHTML=dates.map(d=>`<button type="button" class="date" data-date="${d.iso}" aria-pressed="${d.iso===state.date}" ${locked?"disabled":""} aria-label="${esc(d.long)}"><span>${esc(d.wd)}</span><b>${d.d}</b></button>`).join("");
  const t=$("time");
  t.innerHTML=times().length?times().map(x=>`<option ${x===state.time?"selected":""}>${x}</option>`).join(""):"<option>—</option>";
  t.disabled=locked||!times().length;
  $("guests").textContent=state.guests;
  $("gMinus").disabled=locked||state.guests<=1;$("gPlus").disabled=locked||state.guests>=DATA.maxGuests;
}
$("dates").addEventListener("click",e=>{const b=e.target.closest(".date");if(!b||b.disabled)return;state.date=b.dataset.date;state.notice=null;state.sel=null;loadAvail();});
$("time").addEventListener("change",e=>{state.time=e.target.value;changed();});
$("gMinus").onclick=()=>{state.guests=Math.max(1,state.guests-1);changed();};
$("gPlus").onclick=()=>{state.guests=Math.min(DATA.maxGuests,state.guests+1);changed();};
function changed(){state.notice=null;if(state.sel&&!isAvail(byId[state.sel]))state.sel=null;render();}
document.querySelector(".controls").addEventListener("click",e=>{if(e.target.closest("button:disabled,select:disabled")&&state.hold)toast("Release your held spot to change the date, time or guests.");});

/* ---------- View toggle & list ---------- */
function setView(v){state.view=v;$("vMap").setAttribute("aria-pressed",v==="map");$("vList").setAttribute("aria-pressed",v==="list");$("list").hidden=v!=="list";$("zoomBox").hidden=v!=="map";renderList();}
$("vMap").onclick=()=>setView("map");$("vList").onclick=()=>setView("list");
function renderList(){
  if(state.view!=="list")return;
  $("list").innerHTML=zones.map(z=>{
    const av=spots.filter(s=>s.zone===z&&statusOf(s)!=="unavailable");
    return `<h3>${esc(z)}</h3>`+(av.length?`<div class="chips">${av.map(s=>`<button type="button" class="chip" data-id="${s.id}" aria-pressed="${statusOf(s)==="selected"}"><b>${esc(nameOf(s))}</b><span>${seatsText(s)} guests</span></button>`).join("")}</div>`:`<p class="empty-list">Nothing free here${state.time?` at ${state.time}`:""}.</p>`);
  }).join("");
}
$("list").addEventListener("click",e=>{const b=e.target.closest(".chip");if(b)onSpot(b.dataset.id);});

/* ---------- Interactions ---------- */
function toast(msg){const t=$("toast");t.textContent=msg;t.classList.add("show");clearTimeout(toast.t);toast.t=setTimeout(()=>t.classList.remove("show"),2800);}
function onSpot(id){
  if(state.step==="done")return;
  const s=byId[id];
  if(state.hold){if(state.hold.id!==id)toast("Release your held spot to pick a different one.");return;}
  if(!isAvail(s)){toast(state.time?(ruleReason(s,state.time)||`${nameOf(s)} isn't available at ${state.time}.`):"No arrival times left on this day.");return;}
  state.notice=null;state.sel=state.sel===id?null:id;state.collapsed=false;render();
}
// Swipe the phone sheet down, or tap the map outside it: a picked spot is
// un-picked; otherwise the sheet tucks down to its handle (tap to reopen).
// A held spot stays held either way.
function dismissPanel(){
  if(state.step==="browse"&&state.sel&&!state.hold){state.sel=null;state.notice=null;render();return;}
  if(!isDesk()&&!state.collapsed){state.collapsed=true;renderPanel();}
}
function endTime(t,dur){const m=(hourOf(t)*60+ +t.slice(3)+dur)%(24*60);return `${pad(Math.floor(m/60))}:${pad(m%60)}`;}
const dateLabel=()=>(dateObj(state.date)||{}).label||"";
async function post(url,data){
  try{const r=await fetch(url,{method:"POST",body:new URLSearchParams(data)});return {ok:r.ok,status:r.status,body:await r.json().catch(()=>({}))};}
  catch(_){return {ok:false,status:0,body:{error:"Couldn't reach us. Check your connection and try again."}};}
}
async function holdSpot(btn){
  const s=byId[state.sel];btn.disabled=true;btn.textContent="Holding…";
  const r=await post("/book/hold",{table:s.n,date:state.date,time:state.time,guests:state.guests});
  if(r.status===409){conflict(s);return;}
  if(!r.ok){btn.disabled=false;btn.textContent=`Hold this ${noun(s)} for 5 minutes`;state.notice=r.body.error||"Something went wrong.";renderPanel();return;}
  state.hold={id:s.id,token:r.body.token,end:r.body.end,expires:Date.now()+r.body.expiresIn*1000};state.step="details";
  render();$("panel").scrollTop=0;
  const f=$("fName");if(f)f.focus({preventScroll:true});
}
function conflict(s){
  (state.avail.taken[state.time]=state.avail.taken[state.time]||[]).push(s.n);state.sel=null;
  state.notice=`This ${noun(s)} was just reserved. Please select another available ${noun(s)}.`;
  flash(s);render();
}
function release(msg){
  if(state.hold)post("/book/release",{token:state.hold.token});
  state.hold=null;state.sel=null;state.step="browse";state.notice=msg||null;loadAvail();
}
async function confirmBooking(form,btn){
  const f=Object.fromEntries(new FormData(form));const errors={};
  if(!f.name.trim())errors.name="Enter the name for the booking.";
  if((f.phone.match(/\d/g)||[]).length<7)errors.phone="Enter a phone number we can reach you on.";
  if(f.email&&!/^\S+@\S+\.\S+$/.test(f.email))errors.email="Check the email address.";
  ["name","phone","email"].forEach(k=>{$("e_"+k).textContent=errors[k]||"";form.elements[k].setAttribute("aria-invalid",errors[k]?"true":"false");});
  if(Object.keys(errors).length){form.elements[Object.keys(errors)[0]].focus();return;}
  const s=byId[state.hold.id];btn.disabled=true;btn.textContent="Confirming…";
  const r=await post("/book/confirm",Object.assign({token:state.hold.token},f));
  if(r.status===410){state.hold=null;release(r.body.error);return;}
  if(!r.ok){btn.disabled=false;btn.textContent="Confirm booking";$("e_form").textContent=r.body.error||"Something went wrong.";return;}
  const b=r.body;
  state.booking={code:b.code,s,date:b.date,time:b.time,end:b.end,guests:b.guests,name:b.name,dep:depositOf(s)};
  state.hold=null;state.sel=null;state.step="done";loadAvail();$("panel").scrollTop=0;
}

/* ---------- Panel ---------- */
function suggestions(){
  const ts=times(),i=Math.max(0,ts.indexOf(state.time));const out=[];
  for(let d=1;d<ts.length&&out.length<3;d++)for(const j of [i+d,i-d])if(ts[j]&&availCount(ts[j])>0&&out.length<3)out.push(ts[j]);
  return out.sort();
}
const callLine=()=>PHONE?`<p class="fine">Prefer to call? <a href="tel:${esc(PHONE)}">${esc(PHONE)}</a></p>`:"";
function renderPanel(){
  const p=$("panel");p.classList.toggle("collapsed",!!state.collapsed&&!isDesk());const notice=state.notice?`<div class="notice" role="alert">${esc(state.notice)}</div>`:"";
  if(state.step==="browse"&&!state.sel){
    const n=availCount();const g=`${state.guests} ${state.guests===1?"guest":"guests"}`;
    let body=`${notice}<div class="countrow"><p class="big">${n}</p><p><strong>${n===1?"spot":"spots"} free for ${g}</strong><br><span class="muted">${esc(dateLabel())}${state.time?` at ${state.time}`:""}</span></p></div>`;
    if(!spots.length)body+=`<p>Online booking isn't set up yet.</p>`;
    else if(!times().length)body+=`<p>No more arrival times on this day. Try another date.</p>`;
    else if(n===0){
      const sug=suggestions();
      const lounge=state.guests>4&&spots.some(s=>s.kind==="lounge"&&s.max>=state.guests)?` Groups of 5 to 8 can book a terrace lounge from 18:00.`:"";
      body+=sug.length?`<p>Nothing fits at ${state.time}.${lounge} Try one of these times:</p><div class="suggest">${sug.map(t=>`<button type="button" data-time="${t}">${t}</button>`).join("")}</div>`:`<p>Nothing fits on this day for ${g}. Try another date.</p>`;
    }else body+=`<p class="hint" style="margin:0">Tap a gold spot to pick it. Pinch or scroll to zoom.</p>`;
    p.innerHTML=body+callLine();
  }else if(state.step==="browse"){
    const s=byId[state.sel];const dep=depositOf(s);
    p.innerHTML=`${notice}<h2>${esc(nameOf(s))}</h2><p class="muted">${esc(s.zone)}</p>
      <dl class="facts">
        <div><dt>Guests</dt><dd>${seatsText(s)}</dd></div>
        <div><dt>Time</dt><dd>${slotText(s,state.time)}</dd></div>
        <div><dt>${feeName(s)}</dt><dd>${dep?money.format(dep):"None"}</dd></div>
        ${s.minSpend?`<div><dt>Minimum spend</dt><dd>${money.format(s.minSpend)}</dd></div>`:`<div><dt>Date</dt><dd>${esc(dateLabel())}</dd></div>`}
      </dl>
      <button type="button" class="btn" id="bHold">Hold this ${noun(s)} for 5 minutes</button>
      <button type="button" class="btn ghost" id="bClear">Clear selection</button>`;
    $("bHold").onclick=e=>holdSpot(e.currentTarget);
    $("bClear").onclick=()=>{state.sel=null;render();};
  }else if(state.step==="details"){
    const s=byId[state.hold.id];const dep=depositOf(s);
    p.innerHTML=`<div class="timer"><span>Held for you</span><strong id="cd">5:00</strong></div>
      <h2>Your details</h2>
      <p class="summary">${esc(nameOf(s))}, ${esc(dateLabel())}, ${slotText(s,state.time,state.hold.end)}, ${state.guests} ${state.guests===1?"guest":"guests"}</p>
      <form id="form" novalidate>
        <label>Name<input name="name" id="fName" autocomplete="name" maxlength="80" required aria-describedby="e_name"><span class="err" id="e_name"></span></label>
        <label>Phone<input name="phone" type="tel" autocomplete="tel" inputmode="tel" maxlength="30" required aria-describedby="e_phone"><span class="err" id="e_phone"></span></label>
        <label>Email <span class="opt">(optional, for your confirmation)</span><input name="email" type="email" autocomplete="email" maxlength="120" aria-describedby="e_email"><span class="err" id="e_email"></span></label>
        <label>Special requests <span class="opt">(optional)</span><textarea name="notes" maxlength="500" placeholder="Birthday, allergies, shade for a baby…"></textarea></label>
        ${s.minSpend?`<p class="muted" style="margin:0">Minimum spend of ${money.format(s.minSpend)} applies. Your deposit counts towards it.</p>`:""}
        ${dep?`<div class="pay"><span>${feeName(s)}, paid at the venue</span><span>${money.format(dep)}</span></div>`:""}
        <span class="err" id="e_form" role="alert"></span>
        <p class="fine" style="margin:0">Used only to manage your booking. <a href="/privacy" target="_blank">Privacy</a></p>
        <button type="submit" class="btn" id="bConfirm">Confirm booking</button>
      </form>
      <button type="button" class="btn ghost" id="bRelease">Release this ${noun(s)}</button>
      ${dep?`<p class="fine">No payment is taken online.</p>`:""}`;
    $("form").onsubmit=e=>{e.preventDefault();confirmBooking(e.currentTarget,$("bConfirm"));};
    $("bRelease").onclick=()=>release(`${nameOf(s)} was released.`);
    tick();
  }else{
    const b=state.booking;
    p.innerHTML=`<h2>You're booked, ${esc(b.name.split(" ")[0])}</h2>
      <p class="muted" style="margin:10px 0 0">Booking code</p>
      <p class="bookcode">${esc(b.code)}</p>
      <dl class="facts">
        <div><dt>Spot</dt><dd>${esc(nameOf(b.s))}</dd></div>
        <div><dt>Guests</dt><dd>${b.guests}</dd></div>
        <div><dt>Date</dt><dd>${esc(b.date)}</dd></div>
        <div><dt>Time</dt><dd>${slotText(b.s,b.time,b.end)}</dd></div>
        ${b.dep?`<div><dt>${feeName(b.s)} at the venue</dt><dd>${money.format(b.dep)}</dd></div>`:""}
      </dl>
      <p>Show this code at the entrance. Once you're seated, scan the QR code on your ${noun(b.s)} to order food and drinks.</p>
      ${PHONE?`<p class="fine">Need to change or cancel? Call <a href="tel:${esc(PHONE)}">${esc(PHONE)}</a>.</p>`:""}
      <button type="button" class="btn" id="bAgain">Book another spot</button>`;
    $("bAgain").onclick=()=>{state.step="browse";state.booking=null;state.notice=null;render();};
  }
  p.querySelectorAll(".suggest button").forEach(b=>b.onclick=()=>{state.time=b.dataset.time;changed();});
}
function render(){renderControls();paintSpots();renderList();renderPanel();}

(function swipeSheet(){
  const p=$("panel");let y0=null,dy=0;
  p.addEventListener("touchstart",e=>{if(isDesk()||state.collapsed||p.scrollTop>0)return;y0=e.touches[0].clientY;dy=0;p.classList.add("dragging");},{passive:true});
  p.addEventListener("touchmove",e=>{if(y0===null)return;dy=Math.max(0,e.touches[0].clientY-y0);p.style.transform=`translateY(${dy}px)`;},{passive:true});
  const end=()=>{if(y0===null)return;y0=null;p.classList.remove("dragging");p.style.transform="";if(dy>70)dismissPanel();};
  p.addEventListener("touchend",end);p.addEventListener("touchcancel",end);
  p.addEventListener("click",()=>{if(state.collapsed){state.collapsed=false;renderPanel();}});
})();

/* ---------- Hold countdown ---------- */
function tick(){
  if(!state.hold)return;
  const left=state.hold.expires-Date.now();
  if(left<=0){const s=byId[state.hold.id];release(`Your 5-minute hold on ${nameOf(s)} ended and it was released. Pick a spot to start again.`);return;}
  const el=$("cd");if(el){const sec=Math.ceil(left/1000);el.textContent=`${Math.floor(sec/60)}:${pad(sec%60)}`;}
}
setInterval(tick,1000);

/* ---------- Live: other guests' bookings show up as they happen ---------- */
setInterval(()=>{if(!document.hidden&&state.step==="browse")loadAvail(true);},15000);
// Leaving mid-hold gives the spot straight back instead of blocking it for 5 minutes.
addEventListener("pagehide",()=>{if(state.hold)navigator.sendBeacon("/book/release",new URLSearchParams({token:state.hold.token}));});

render();requestAnimationFrame(fit);loadAvail();
})();
