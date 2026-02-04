document.addEventListener('DOMContentLoaded', () => {
    // --- Type Mappings ---
    const QTypeMap = {
        1: 'A',
        2: 'NS',
        5: 'CNAME',
        6: 'SOA',
        12: 'PTR',
        15: 'MX',
        16: 'TXT',
        28: 'AAAA',
        33: 'SRV',
        41: 'OPT',
        65: 'HTTPS',
        255: 'ANY'
    };

    const RCodeMap = {
        0: 'NOERROR',
        1: 'FORMERR',
        2: 'SERVFAIL',
        3: 'NXDOMAIN',
        4: 'NOTIMP',
        5: 'REFUSED',
        6: 'YXDOMAIN',
        7: 'YXRRSET',
        8: 'NXRRSET',
        9: 'NOTAUTH',
        10: 'NOTZONE'
    };

    const getQTypeName = (code) => QTypeMap[code] || `TYPE${code}`;
    const getRCodeName = (code) => RCodeMap[code] || `RCODE${code}`;

    // --- State ---
    const state = {
        logs: [],
        total: 0,
        stats: {
            avg_latency_1d: 0,
            avg_latency_7d: 0,
            upstream_avg_latency_1d: 0,
            upstream_avg_latency_7d: 0
        },
        page: 1,
        pageSize: 50,
        search: '',
        filters: {
            startTime: '',
            endTime: '',
            type: '',
            rCode: '',
            clientIp: ''
        },
        sortField: 'time',
        sortOrder: 'desc'
    };

    // --- Elements ---
    const elements = {
        lat1d: document.getElementById('lat-1d'),
        lat7d: document.getElementById('lat-7d'),
        upLat1d: document.getElementById('up-lat-1d'),
        upLat7d: document.getElementById('up-lat-7d'),

        logsBody: document.getElementById('logs-body'),

        prevBtn: document.getElementById('prev-page'),
        nextBtn: document.getElementById('next-page'),
        pageInfo: document.getElementById('page-info'),
        pageSize: document.getElementById('page-size'),
        searchInput: document.getElementById('log-search'),
        refreshBtn: document.getElementById('refresh-logs'),

        filterStart: document.getElementById('filter-start'),
        filterEnd: document.getElementById('filter-end'),
        resetFiltersBtn: document.getElementById('reset-filters'),

        // Desktop Overlay Filters
        headerTypeFilter: document.getElementById('header-type-filter'),
        headerRCodeFilter: document.getElementById('header-rcode-filter'),
        headerIpFilter: document.getElementById('header-ip-filter'),

        // Mobile Filters
        mobileIpFilter: document.getElementById('mobile-ip-filter'),
        mobileTypeFilter: document.getElementById('mobile-type-filter'),
        mobileRCodeFilter: document.getElementById('mobile-rcode-filter'),

        sortHeaders: document.querySelectorAll('.sortable'),
        sortIconTime: document.getElementById('sort-icon-time'),
        sortIconLatency: document.getElementById('sort-icon-latency')
    };

    // --- API Interactions ---
    async function fetchStats() {
        try {
            const res = await fetch('/api/stats');
            const data = await res.json();
            state.stats = data;
            updateStatsUI();
        } catch (e) {
            console.error('Failed to fetch stats', e);
        }
    }

    async function fetchClients() {
        try {
            const res = await fetch('/api/clients');
            const clients = await res.json();

            const populate = (select, current) => {
                if (!select) return;
                select.innerHTML = '<option value="">全部</option>';
                if (clients && clients.length > 0) {
                    clients.forEach(ip => {
                        const opt = document.createElement('option');
                        opt.value = ip;
                        opt.textContent = ip;
                        if (ip === current) opt.selected = true;
                        select.appendChild(opt);
                    });
                }
            };

            populate(elements.headerIpFilter, state.filters.clientIp);
            populate(elements.mobileIpFilter, state.filters.clientIp);

        } catch (e) {
            console.error('Failed to fetch clients', e);
        }
    }


    async function fetchQTypes() {
        try {
            const res = await fetch('/api/qtypes');
            const types = await res.json();

            const populate = (select, current) => {
                if (!select) return;
                select.innerHTML = '<option value="">全部</option>';
                if (types && types.length > 0) {
                    types.forEach(t => {
                        const opt = document.createElement('option');
                        opt.value = t;
                        opt.textContent = getQTypeName(t);
                        if (String(t) === String(current)) opt.selected = true;
                        select.appendChild(opt);
                    });
                }
            };

            populate(elements.headerTypeFilter, state.filters.type);
            populate(elements.mobileTypeFilter, state.filters.type);

        } catch (e) {
            console.error('Failed to fetch qtypes', e);
        }
    }

    async function fetchRCodes() {
        try {
            const res = await fetch('/api/rcodes');
            const data = await res.json();

            const populate = (select, current) => {
                if (!select) return;
                select.innerHTML = '<option value="">全部</option>';
                if (data && data.length > 0) {
                    data.forEach(rc => {
                        const opt = document.createElement('option');
                        opt.value = rc;
                        opt.textContent = getRCodeName(rc);
                        if (String(rc) === String(current)) opt.selected = true;
                        select.appendChild(opt);
                    });
                }
            };

            populate(elements.headerRCodeFilter, state.filters.rCode);
            populate(elements.mobileRCodeFilter, state.filters.rCode);

        } catch (e) {
            console.error('Failed to fetch rcodes', e);
        }
    }

    async function fetchLogs() {
        let sortParam = `${state.sortField}_${state.sortOrder}`;

        const params = new URLSearchParams({
            page: state.page,
            page_size: state.pageSize,
            search: state.search,
            sort: sortParam
        });

        if (state.filters.startTime) params.append('start_time', new Date(state.filters.startTime).toISOString());
        if (state.filters.endTime) params.append('end_time', new Date(state.filters.endTime).toISOString());
        if (state.filters.type) params.append('type', state.filters.type);
        if (state.filters.rCode) params.append('r_code', state.filters.rCode);
        if (state.filters.clientIp) params.append('client_ip', state.filters.clientIp);

        if (state.logs.length === 0 && elements.logsBody) {
            elements.logsBody.innerHTML = '<tr><td colspan="6" style="text-align:center; padding: 20px; color: #718096;">正在加载日志...</td></tr>';
        }

        try {
            const res = await fetch(`/api/logs?${params.toString()}`);

            if (!res.ok) {
                try {
                    const errData = await res.json();
                    throw new Error(errData.error || res.statusText);
                } catch (jsonErr) {
                    throw new Error(res.statusText + ' (' + res.status + ')');
                }
            }

            const data = await res.json();

            state.logs = data.logs || [];
            state.total = data.total || 0;
            state.pageSize = data.page_size || 50;

            if (elements.pageSize && elements.pageSize.value != state.pageSize) {
                elements.pageSize.value = state.pageSize;
            }

            renderLogs();
            updatePaginationUI();
            updateSortIcons();

            // Sync filters if changed externally (e.g. initial load or reset)
            syncFilterUI();

        } catch (e) {
            console.error('Failed to fetch logs', e);
            if (elements.logsBody) {
                elements.logsBody.innerHTML = `<tr><td colspan="6" style="text-align:center; color: var(--danger-color);">加载日志失败: ${e.message}</td></tr>`;
            }
        }
    }

    function syncFilterUI() {
        // Sync desktop and mobile filters with state
        if (elements.headerIpFilter) elements.headerIpFilter.value = state.filters.clientIp;
        if (elements.mobileIpFilter) elements.mobileIpFilter.value = state.filters.clientIp;

        if (elements.headerTypeFilter) elements.headerTypeFilter.value = state.filters.type;
        if (elements.mobileTypeFilter) elements.mobileTypeFilter.value = state.filters.type;

        if (elements.headerRCodeFilter) elements.headerRCodeFilter.value = state.filters.rCode;
        if (elements.mobileRCodeFilter) elements.mobileRCodeFilter.value = state.filters.rCode;

        if (elements.filterStart) elements.filterStart.value = state.filters.startTime;
        if (elements.filterEnd) elements.filterEnd.value = state.filters.endTime;
        if (elements.searchInput) elements.searchInput.value = state.search;
    }

    // --- UI UI ---
    function updateSortIcons() {
        if (!elements.sortIconTime || !elements.sortIconLatency) return;

        elements.sortIconTime.textContent = '↕';
        elements.sortIconLatency.textContent = '↕';
        elements.sortIconTime.style.opacity = '0.3';
        elements.sortIconLatency.style.opacity = '0.3';

        let activeIcon = null;
        if (state.sortField === 'time') activeIcon = elements.sortIconTime;
        if (state.sortField === 'latency') activeIcon = elements.sortIconLatency;

        if (activeIcon) {
            activeIcon.textContent = state.sortOrder === 'asc' ? '↑' : '↓';
            activeIcon.style.opacity = '1';
        }
    }

    function updatePaginationUI() {
        if (!elements.pageInfo || !elements.prevBtn || !elements.nextBtn) return;

        const totalPages = Math.ceil(state.total / state.pageSize);
        elements.pageInfo.textContent = `第 ${state.page} 页 / 共 ${totalPages || 1} 页 (共 ${state.total} 条)`;

        elements.prevBtn.disabled = state.page <= 1;
        elements.nextBtn.disabled = state.page >= totalPages || totalPages === 0;
    }

    function renderLogs() {
        if (!elements.logsBody) return;

        if (state.logs.length === 0) {
            elements.logsBody.innerHTML = '<tr><td colspan="6" style="text-align:center; padding: 20px; color: #718096;">没有找到日志。</td></tr>';
            return;
        }

        elements.logsBody.innerHTML = state.logs.map(log => {
            const date = new Date(log.time);
            const timeStr = date.toLocaleString('zh-CN');

            let latencyStr = '';
            let latencyClass = '';

            if (log.elapsed < 1000) {
                // < 1ms (microsecond level) -> Green
                latencyStr = `${log.elapsed}µs`;
                latencyClass = 'latency-micros';
            } else {
                const ms = log.elapsed / 1000;
                latencyStr = `${ms.toFixed(2)}ms`;

                if (ms < 100) {
                    // 1ms - 100ms -> Blue/Cyan
                    latencyClass = 'latency-fast';
                } else if (ms < 300) {
                    // 100ms - 300ms -> Orange
                    latencyClass = 'latency-avg';
                } else {
                    // > 300ms -> Red
                    latencyClass = 'latency-slow';
                }
            }

            return `
                <tr>
                    <td class="col-time" data-label="时间">${timeStr}</td>
                    <td class="col-ip" data-label="客户端 IP"><span class="clickable-ip" onclick="window.filterByIP('${log.client_ip}')" title="点击筛选 IP">${log.client_ip}</span></td>
                    <td class="col-domain" data-label="域名">${log.q_name}</td>
                    <td class="col-type" data-label="类型">${getQTypeName(log.q_type)}</td>
                    <td class="col-rcode" data-label="RCode">${getRCodeName(log.r_code)}</td>
                    <td class="col-latency ${latencyClass}" data-label="耗时">${latencyStr}</td>
                </tr>
            `;
        }).join('');
    }

    // --- Event Handlers ---
    elements.sortHeaders.forEach(th => {
        th.addEventListener('click', () => {
            const field = th.dataset.sort;
            if (state.sortField === field) {
                state.sortOrder = state.sortOrder === 'asc' ? 'desc' : 'asc';
            } else {
                state.sortField = field;
                state.sortOrder = 'desc';
            }
            state.page = 1;
            fetchLogs();
        });
    });

    const handleFilterChange = (key, value) => {
        state.filters[key] = value;
        state.page = 1;
        fetchLogs();
    };

    // Desktop Filters
    if (elements.headerIpFilter) elements.headerIpFilter.addEventListener('change', (e) => handleFilterChange('clientIp', e.target.value));
    if (elements.headerTypeFilter) elements.headerTypeFilter.addEventListener('change', (e) => handleFilterChange('type', e.target.value));
    if (elements.headerRCodeFilter) elements.headerRCodeFilter.addEventListener('change', (e) => handleFilterChange('rCode', e.target.value));

    // Mobile Filters
    if (elements.mobileIpFilter) elements.mobileIpFilter.addEventListener('change', (e) => handleFilterChange('clientIp', e.target.value));
    if (elements.mobileTypeFilter) elements.mobileTypeFilter.addEventListener('change', (e) => handleFilterChange('type', e.target.value));
    if (elements.mobileRCodeFilter) elements.mobileRCodeFilter.addEventListener('change', (e) => handleFilterChange('rCode', e.target.value));


    window.filterByIP = (ip) => {
        handleFilterChange('clientIp', ip);
    };

    if (elements.prevBtn) {
        elements.prevBtn.addEventListener('click', () => {
            if (state.page > 1) { state.page--; fetchLogs(); }
        });
    }

    if (elements.nextBtn) {
        elements.nextBtn.addEventListener('click', () => {
            state.page++; fetchLogs();
        });
    }

    if (elements.refreshBtn) {
        elements.refreshBtn.addEventListener('click', () => {
            fetchStats();
            fetchClients();
            fetchQTypes();
            fetchRCodes();
            fetchLogs();
        });
    }

    if (elements.pageSize) {
        elements.pageSize.addEventListener('change', (e) => {
            state.pageSize = parseInt(e.target.value);
            state.page = 1;
            fetchLogs();
        });
    }

    let searchTimeout;
    if (elements.searchInput) {
        elements.searchInput.addEventListener('input', (e) => {
            clearTimeout(searchTimeout);
            searchTimeout = setTimeout(() => {
                state.search = e.target.value;
                state.page = 1;
                fetchLogs();
            }, 300);
        });
    }

    if (elements.filterStart) elements.filterStart.addEventListener('change', (e) => handleFilterChange('startTime', e.target.value));
    if (elements.filterEnd) elements.filterEnd.addEventListener('change', (e) => handleFilterChange('endTime', e.target.value));

    if (elements.resetFiltersBtn) {
        elements.resetFiltersBtn.addEventListener('click', () => {
            state.page = 1;
            state.search = '';
            state.filters = { startTime: '', endTime: '', clientIp: '', type: '', rCode: '' };
            state.sortField = 'time';
            state.sortOrder = 'desc';
            fetchLogs();
        });
    }

    function updateStatsUI() {
        if (!elements.lat1d) return;
        const s = state.stats;
        elements.lat1d.textContent = s.avg_latency_1d.toFixed(2) + 'ms';
        elements.lat7d.textContent = s.avg_latency_7d.toFixed(2) + 'ms';
        elements.upLat1d.textContent = s.upstream_avg_latency_1d.toFixed(2) + 'ms';
        elements.upLat7d.textContent = s.upstream_avg_latency_7d.toFixed(2) + 'ms';
    }

    // Init
    fetchStats();
    fetchClients();
    fetchQTypes();
    fetchRCodes();
    fetchLogs();
});
