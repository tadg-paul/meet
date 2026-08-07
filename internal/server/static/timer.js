// ABOUTME: Client for the shared per-room meeting timer (issue #15). Subscribes
// ABOUTME: to the SSE stream, renders the banner and numerals, plays the cues
// ABOUTME: (muting the local mic for each), and gives moderators the controls.

window.initTimer = (api, jwt) => {
    const room = decodeURIComponent(location.pathname.replace(/^\/+/, '').replace(/\/+$/, ''));
    if (!room) {
        return;
    }
    const isModerator = Boolean(jwt);
    const FLASH = 10;

    const banner = document.querySelector('.banner');
    const wrap = document.getElementById('timer');
    const numEl = document.getElementById('timer-num');
    const ctrlsEl = document.getElementById('timer-ctrls');
    const configEl = document.getElementById('timer-config');

    // --- audio cues ---
    const sounds = {
        start: new Audio('/static/sounds/timer-start.m4a'),
        pause: new Audio('/static/sounds/pause.m4a'),
        resume: new Audio('/static/sounds/un-pause.m4a'),
        warning: new Audio('/static/sounds/early-warning.mp3'),
        end: new Audio('/static/sounds/timer-end.mp3'),
        graceEnd: new Audio('/static/sounds/grace-end-period.mp3'),
    };

    // Arm audio on the first available user gesture / join so a later cue is not
    // blocked by the browser autoplay policy.
    let armed = false;
    const armAudio = () => {
        if (armed) {
            return;
        }
        armed = true;
        Object.values(sounds).forEach((audio) => {
            audio.muted = true;
            const p = audio.play();
            if (p && p.then) {
                p.then(() => {
                    audio.pause();
                    audio.currentTime = 0;
                    audio.muted = false;
                }).catch(() => {
                    audio.muted = false;
                });
            }
        });
    };
    document.addEventListener('pointerdown', armAudio, { once: true });
    document.addEventListener('keydown', armAudio, { once: true });
    api.addEventListener('videoConferenceJoined', armAudio);

    // Play a cue and mute this participant's own microphone for its duration so
    // the cue is not captured and re-broadcast (issue #15, AC15.15, all cues).
    const muteForCue = (audio) => {
        if (!api || !api.isAudioMuted) {
            return;
        }
        api.isAudioMuted().then((muted) => {
            if (muted) {
                return; // already muted: leave it
            }
            api.executeCommand('toggleAudio');
            const restore = () => {
                api.executeCommand('toggleAudio');
                audio.removeEventListener('ended', restore);
            };
            audio.addEventListener('ended', restore);
        }).catch(() => {});
    };

    const playCue = (audio) => {
        if (!audio) {
            return;
        }
        audio.currentTime = 0;
        const p = audio.play();
        if (p && p.catch) {
            p.catch(() => {});
        }
        muteForCue(audio);
    };

    // --- phase computation (mirror of the server) ---
    const compute = (elapsed, cfg, extended) => {
        const total = cfg.total;
        const warnAt = total - cfg.warnSeconds;
        const grace = cfg.graceSeconds;
        if (elapsed < warnAt) {
            return { phase: 'before-warning', remaining: total - elapsed, countUp: 0 };
        }
        if (elapsed < total) {
            return { phase: 'after-warning', remaining: total - elapsed, countUp: 0 };
        }
        const countUp = elapsed - total;
        if (extended || elapsed < total + grace) {
            return { phase: 'grace', remaining: 0, countUp };
        }
        if (elapsed < total + grace + FLASH) {
            return { phase: 'exceeded', remaining: 0, countUp };
        }
        return { phase: 'stopped', remaining: 0, countUp: 0 };
    };

    // --- anchored local clock ---
    let anchor = null; // { baseElapsed, baseAt, running, cfg, extended }
    const localElapsed = () => {
        if (!anchor) {
            return 0;
        }
        if (!anchor.running) {
            return anchor.baseElapsed;
        }
        return anchor.baseElapsed + (performance.now() - anchor.baseAt) / 1000;
    };

    // --- rendering ---
    const fmt = (secs) => {
        const total = Math.max(0, Math.floor(secs));
        const m = Math.floor(total / 60);
        const s = total % 60;
        return `${m}:${String(s).padStart(2, '0')}`;
    };
    const phaseClasses = ['phase-green', 'phase-amber', 'phase-red', 'phase-exceeded'];
    const phaseClass = {
        'before-warning': 'phase-green',
        'after-warning': 'phase-amber',
        grace: 'phase-red',
        exceeded: 'phase-exceeded',
    };

    let flashTimer = null;
    const setBanner = (phase) => {
        banner.classList.remove(...phaseClasses, 'flash-on');
        if (flashTimer) {
            clearInterval(flashTimer);
            flashTimer = null;
        }
        const cls = phaseClass[phase];
        if (cls) {
            banner.classList.add(cls);
        }
        if (phase === 'exceeded') {
            let on = true;
            banner.classList.add('flash-on');
            flashTimer = setInterval(() => {
                on = !on;
                banner.classList.toggle('flash-on', on);
            }, 500);
        }
    };

    let prev = null; // { phase, active, running }
    const playCues = (from, to) => {
        if (!from) {
            return; // first state primes; no cue on late join
        }
        const wasActive = from.phase !== 'stopped';
        const isActive = to.phase !== 'stopped';
        if (!wasActive && isActive) {
            playCue(sounds.start);
            return;
        }
        if (!isActive) {
            return;
        }
        if (from.running && !to.running) {
            playCue(sounds.pause);
        } else if (!from.running && to.running) {
            playCue(sounds.resume);
        }
        if (from.phase === 'before-warning' && to.phase === 'after-warning') {
            playCue(sounds.warning);
        }
        if ((from.phase === 'before-warning' || from.phase === 'after-warning') && to.phase === 'grace') {
            playCue(sounds.end);
        }
        if (from.phase === 'grace' && to.phase === 'exceeded') {
            playCue(sounds.graceEnd);
        }
    };

    const render = () => {
        if (!anchor) {
            return;
        }
        const cfg = anchor.cfg;
        const e = Math.floor(anchor.running ? localElapsed() : anchor.baseElapsed);
        const st = compute(e, cfg, anchor.extended);
        const active = st.phase !== 'stopped';
        const cur = { phase: st.phase, active, running: anchor.running && active };

        playCues(prev, cur);
        prev = cur;

        let show = false;
        let text = '';
        let grey = false;
        if (active) {
            show = true;
            text = st.phase === 'grace' || st.phase === 'exceeded' ? `+${fmt(st.countUp)}` : fmt(st.remaining);
        } else if (isModerator) {
            show = true;
            text = fmt(cfg.total);
            grey = true;
        }
        wrap.style.display = show ? 'flex' : 'none';
        numEl.textContent = text;
        numEl.classList.toggle('grey', grey);

        setBanner(st.phase);
        renderControls(cur);

        if (!active && anchor.running) {
            anchor.running = false; // stop ticking after auto-reset
        }
    };

    // --- moderator controls ---
    const renderControls = (cur) => {
        if (!isModerator) {
            ctrlsEl.innerHTML = '';
            return;
        }
        let btns;
        if (!cur.active) {
            btns = [['SET', openConfig], ['START', () => post('start')]];
        } else if (cur.phase === 'grace' || cur.phase === 'exceeded') {
            btns = cur.running
                ? [['EXTEND', () => post('extend')], ['STOP', () => post('stop')]]
                : [['RESUME', () => post('resume')], ['RESET', () => post('reset')], ['RESTART', () => post('restart')]];
        } else {
            btns = cur.running
                ? [['PAUSE', () => post('pause')]]
                : [['RESUME', () => post('resume')], ['RESET', () => post('reset')], ['RESTART', () => post('restart')]];
        }
        const sig = btns.map((b) => b[0]).join(',');
        if (ctrlsEl.dataset.sig === sig) {
            return;
        }
        ctrlsEl.dataset.sig = sig;
        ctrlsEl.innerHTML = '';
        btns.forEach(([label, fn]) => {
            const b = document.createElement('button');
            b.className = 'timer-btn';
            b.type = 'button';
            b.textContent = label;
            b.addEventListener('click', fn);
            ctrlsEl.appendChild(b);
        });
    };

    const post = async (action, config) => {
        const body = { action };
        if (config) {
            body.config = config;
        }
        try {
            const resp = await fetch(`/${encodeURIComponent(room)}/timer?jwt=${encodeURIComponent(jwt)}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            });
            if (!resp.ok) {
                console.error('timer control failed', action, resp.status);
                return;
            }
            applyState(await resp.json());
        } catch (err) {
            console.error('timer control error', err);
        }
    };

    // --- configuration popover ---
    const parseMMSS = (value) => {
        const m = /^(\d+):([0-5]?\d)$/.exec(value.trim());
        if (!m) {
            return null;
        }
        return Number(m[1]) * 60 + Number(m[2]);
    };

    const buildConfig = () => {
        configEl.innerHTML = `
            <label>Total time
                <div class="row"><input id="tc-total" type="text" inputmode="numeric" placeholder="mm:ss"></div>
            </label>
            <label>Early warning
                <div class="row"><input id="tc-warn" type="number" min="0" max="100"> <span class="computed" id="tc-warn-c"></span></div>
            </label>
            <label>Grace
                <div class="row"><input id="tc-grace" type="number" min="0" max="100"> <span class="computed" id="tc-grace-c"></span></div>
            </label>
            <div class="actions">
                <button class="timer-btn" type="button" id="tc-cancel">CANCEL</button>
                <button class="timer-btn" type="button" id="tc-save">SAVE</button>
            </div>`;
        const totalEl = configEl.querySelector('#tc-total');
        const warnEl = configEl.querySelector('#tc-warn');
        const graceEl = configEl.querySelector('#tc-grace');
        const warnC = configEl.querySelector('#tc-warn-c');
        const graceC = configEl.querySelector('#tc-grace-c');
        const recompute = () => {
            const total = parseMMSS(totalEl.value) || 0;
            const warn = Number(warnEl.value) || 0;
            const grace = Number(graceEl.value) || 0;
            warnC.textContent = `(${fmt(Math.round((total * warn) / 100))})`;
            graceC.textContent = `(${fmt(Math.round((total * grace) / 100))})`;
        };
        [totalEl, warnEl, graceEl].forEach((el) => el.addEventListener('input', recompute));
        configEl.querySelector('#tc-cancel').addEventListener('click', () => { configEl.hidden = true; });
        configEl.querySelector('#tc-save').addEventListener('click', () => {
            const total = parseMMSS(totalEl.value);
            const warn = Number(warnEl.value);
            const grace = Number(graceEl.value);
            if (total === null || total <= 0 || warn < 0 || warn > 100 || grace < 0 || grace > 100) {
                totalEl.style.borderColor = '#e74c3c';
                return;
            }
            configEl.hidden = true;
            post('set', { total, warnPercent: warn, gracePercent: grace });
        });
        return { totalEl, warnEl, graceEl, recompute };
    };

    let configInputs = null;
    const openConfig = () => {
        if (!configInputs) {
            configInputs = buildConfig();
        }
        const cfg = anchor ? anchor.cfg : { total: 900, warnPercent: 20, gracePercent: 30 };
        configInputs.totalEl.value = fmt(cfg.total);
        configInputs.warnEl.value = cfg.warnPercent;
        configInputs.graceEl.value = cfg.gracePercent;
        configInputs.recompute();
        configEl.hidden = false;
    };

    // --- SSE subscription ---
    const applyState = (view) => {
        anchor = {
            baseElapsed: view.elapsed,
            baseAt: performance.now(),
            running: view.running,
            cfg: view.config,
            extended: view.extended,
        };
        render();
    };
    const connect = () => {
        const es = new EventSource(`/${encodeURIComponent(room)}/timer/events`);
        es.onmessage = (ev) => {
            try {
                applyState(JSON.parse(ev.data));
            } catch (err) {
                console.error('timer state parse error', err);
            }
        };
        // EventSource reconnects automatically on error.
    };

    connect();
    setInterval(render, 250);
};
