/**
 * Obsidian Sentinel WAF - Enterprise JavaScript Application
 * Core application logic, routing, and state management
 */

// ============================================
// SECURITY UTILITIES
// ============================================
/**
 * Escape HTML entities to prevent XSS attacks
 * @param {string} str - String to escape
 * @returns {string} Escaped string safe for HTML insertion
 */
function escapeHtml(str) {
    if (str === null || str === undefined) return '';
    const div = document.createElement('div');
    div.textContent = String(str);
    return div.innerHTML;
}

/**
 * Escape HTML attributes
 * @param {string} str - String to escape for attribute use
 * @returns {string} Escaped string safe for HTML attribute insertion
 */
function escapeAttr(str) {
    if (str === null || str === undefined) return '';
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/"/g, '&quot;')
        .replace(/'/g, '&#39;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;');
}

/**
 * Safe text helper - always escapes for HTML content context
 * @param {string} str - String to safely render
 * @returns {string} Escaped string
 */
function safeText(str) {
    return escapeHtml(str);
}

/**
 * Safe attribute helper - always escapes for HTML attribute context
 * @param {string} str - String to safely render in attributes
 * @returns {string} Escaped string for attribute use
 */
function safeAttr(str) {
    return escapeAttr(str);
}

// ============================================
// AUTHENTICATION HELPER
// ============================================
function getStoredValue(key) {
    // Check localStorage first (primary), then sessionStorage (fallback)
    return localStorage.getItem(key) || sessionStorage.getItem(key);
}

function getStoredUser() {
    const userStr = getStoredValue('obsidian_user');
    try {
        return userStr ? JSON.parse(userStr) : null;
    } catch {
        return null;
    }
}

// ============================================
// APPLICATION STATE
// ============================================
const ObsidianApp = {
    token: getStoredValue('obsidian_token'),
    user: getStoredUser(),
    currentView: 'dashboard',
    rulesFilter: 'effective',
    ws: null,
    charts: {},
    refreshInterval: null,

    // Initialize the application
    init() {
        if (!this.token) {
            globalThis.location.href = '/login.html';
            return;
        }
        this.setupNavigation();
        this.setupUserProfile();
        this.connectWebSocket();
        this.loadView('dashboard');
        this.startAutoRefresh();
        this.setupEventListeners();
    },

    // Setup navigation event listeners
    setupNavigation() {
        document.querySelectorAll('[data-view]').forEach(el => {
            el.addEventListener('click', (e) => {
                e.preventDefault();
                this.loadView(el.dataset.view);
            });
        });

        // Hide admin features for non-admins
        if (this.user?.role !== 'Admin') {
            document.querySelectorAll('.admin-only').forEach(el => {
                el.style.display = 'none';
            });
        }
    },

    // Setup user profile display
    setupUserProfile() {
        const nameEl = document.getElementById('user-name');
        const roleEl = document.getElementById('user-role');
        if (nameEl) nameEl.innerText = this.user?.name || 'Admin';
        if (roleEl) roleEl.innerText = this.user?.role || 'Security Officer';
    },

    // Load a view/page
    async loadView(view) {
        this.currentView = view;

        // Update navigation state
        document.querySelectorAll('.nav-link').forEach(el => el.classList.remove('active'));
        const activeLink = document.querySelector(`[data-view="${view}"]`);
        if (activeLink) activeLink.classList.add('active');

        // Update page title
        const titles = {
            dashboard: 'Security Dashboard',
            analytics: 'Analytics & Reports',
            logs: 'Attack Logs',
            rules: 'Rules Management',
            admin: 'Admin Panel',
            threats: 'Threat Intelligence',
            geoip: 'GeoIP Blocking',
            ratelimit: 'Rate Limiting',
            alerts: 'Alert Webhooks',
            security: 'Security Settings',
            health: 'System Health',
            settings: 'Settings'
        };
        document.getElementById('pageTitle').innerText = titles[view] || 'Dashboard';

        // Toggle view visibility
        document.querySelectorAll('.view-section').forEach(el => el.classList.add('d-none'));
        const target = document.getElementById('view-' + view);
        if (target) target.classList.remove('d-none');

        // Load view-specific data
        switch (view) {
            case 'dashboard':
                await this.fetchDashboardData();
                break;
            case 'analytics':
                await this.fetchAnalyticsData();
                break;
            case 'logs':
                await this.fetchLogs();
                break;
            case 'rules':
                await this.fetchRules();
                break;
            case 'admin':
                await this.fetchAdminData();
                break;
            case 'threats':
                await this.fetchThreatIntelligence();
                break;
            case 'geoip':
                await this.fetchGeoIPData();
                break;
            case 'ratelimit':
                await this.fetchRateLimitData();
                break;
            case 'alerts':
                await this.fetchAlertsData();
                break;
            case 'security':
                await this.fetchSecurityData();
                break;
            case 'health':
                await this.fetchHealthData();
                await this.fetchCacheStats();
                break;
        }
    },

    // API helper
    async api(endpoint, options = {}) {
        const headers = {
            'Authorization': 'Bearer ' + this.token,
            'Content-Type': 'application/json',
            ...options.headers
        };
        try {
            const res = await fetch(endpoint, { ...options, headers });
            if (res.status === 401) {
                this.logout();
                return null;
            }
            return await res.json();
        } catch (e) {
            console.error('API Error:', e);
            return null;
        }
    },

    // Fetch dashboard data
    async fetchDashboardData() {
        const [stats, logs, metrics, health] = await Promise.all([
            this.api('/api/stats'),
            this.api('/api/logs'),
            this.api('/api/metrics'),
            this.api('/api/health')
        ]);

        if (stats) {
            const totalThreats = (stats.blocked_requests || 0) + (stats.flagged_requests || 0);
            document.getElementById('total-attacks').innerText = totalThreats;
            document.getElementById('blocked-count').innerText = stats.blocked_requests || 0;
            document.getElementById('flagged-count').innerText = stats.flagged_requests || 0;
            document.getElementById('safe-requests').innerText = stats.safe_requests || 0;
            
            // Calculate and display dynamic delta (compare to stored previous value)
            this.updateThreatsDelta(totalThreats, logs || []);
            
            // Quick Stats - Real Data
            const quickRules = document.getElementById('quick-active-rules');
            if (quickRules) quickRules.innerText = stats.active_rules_count || 0;
            
            const quickTotal = document.getElementById('quick-total-requests');
            if (quickTotal) quickTotal.innerText = stats.total_requests || 0;

            // Pass logs to charts for real timeline data
            this.updateCharts(stats, logs || []);
        }

        if (metrics) {
            const uptimeEl = document.getElementById('quick-uptime');
            if (uptimeEl) {
                const secs = metrics.uptime_seconds || 0;
                const hours = Math.floor(secs / 3600);
                const mins = Math.floor((secs % 3600) / 60);
                uptimeEl.innerText = `${hours}h ${mins}m`;
            }
        }

        if (health) {
            const statusDot = document.getElementById('system-status-dot');
            const statusText = document.getElementById('system-status-text');
            if (health.status === 'healthy') {
                if (statusDot) statusDot.style.backgroundColor = '#10b981';
                if (statusText) statusText.innerText = 'System Operational';
            } else {
                if (statusDot) statusDot.style.backgroundColor = '#f43f5e';
                if (statusText) statusText.innerText = 'System Issue Detected';
            }
        }

        if (logs && logs.length > 0) {
            this.renderRecentLogs(logs.slice(-10).reverse());
        } else {
            const tbody = document.getElementById('logs-table');
            if (tbody) tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-4">No requests logged yet. Navigate around to see logs.</td></tr>';
        }
    },

    // Render recent logs with status classification
    renderRecentLogs(logs) {
        const tbody = document.getElementById('logs-table');
        if (!tbody) return;

        tbody.innerHTML = logs.map(log => {
            const date = new Date(log.timestamp).toLocaleTimeString();
            
            // Status badge based on log status (safe - hardcoded values)
            let statusBadge = '';
            switch (log.status) {
                case 'Blocked':
                    statusBadge = '<span class="badge bg-danger">Blocked</span>';
                    break;
                case 'ThreatBlocked':
                    statusBadge = '<span class="badge bg-danger"><i class="fas fa-skull-crossbones me-1"></i>Threat</span>';
                    break;
                case 'Flagged':
                    statusBadge = '<span class="badge bg-warning text-dark">Flagged</span>';
                    break;
                case 'Safe':
                default:
                    statusBadge = '<span class="badge bg-success">Safe</span>';
                    break;
            }

            // Action badge - escape user-controlled data
            const safeAction = escapeHtml(log.action || 'Unknown');
            const actionBadge = log.action === 'Pass' 
                ? '<span class="badge bg-dark border border-success text-success">Pass</span>'
                : `<span class="badge bg-dark border border-danger text-danger">${safeAction}</span>`;

            // Calculate row class based on status
            let rowClass = '';
            if (log.status === 'Blocked' || log.status === 'ThreatBlocked') {
                rowClass = 'table-danger';
            } else if (log.status === 'Flagged') {
                rowClass = 'table-warning';
            }

            // Format URI display with truncation - escape user-controlled data
            const rawUri = String(log.uri || '/');
            const uriDisplay = escapeHtml(rawUri.substring(0, 30));
            const uriSuffix = rawUri.length > 30 ? '...' : '';
            
            // Format details display with truncation - escape user-controlled data
            const rawDetails = String(log.details || '');
            const detailsDisplay = escapeHtml(rawDetails.substring(0, 40));
            const detailsSuffix = rawDetails.length > 40 ? '...' : '';

            // Escape other user-controlled fields
            const safeRuleId = escapeHtml(log.rule_id || '-');
            const safeMethod = escapeHtml(log.method || 'GET');

            return `
                <tr class="${rowClass}">
                    <td class="text-secondary font-monospace small">${escapeHtml(date)}</td>
                    <td>${statusBadge}</td>
                    <td>${actionBadge}</td>
                    <td class="font-monospace small">${safeRuleId}</td>
                    <td><code class="text-info">${safeMethod} ${uriDisplay}${uriSuffix}</code></td>
                    <td class="text-white-50 small">${detailsDisplay}${detailsSuffix}</td>
                </tr>
            `;
        }).join('');
    },

    // Update charts
    updateCharts(stats, logs = []) {
        const ctx = document.getElementById('attackChart')?.getContext('2d');
        if (!ctx) return;

        // Calculate timeline data from logs (threats per hour for last 6 hours)
        const timelineData = this.calculateTimelineData(logs);

        if (this.charts.attack) {
            this.charts.attack.data.datasets[0].data = [
                stats.blocked_requests || 0,
                stats.flagged_requests || 0,
                stats.safe_requests || 0
            ];
            this.charts.attack.update();
        } else {
            this.charts.attack = new Chart(ctx, {
                type: 'doughnut',
                data: {
                    labels: ['Blocked', 'Flagged', 'Safe'],
                    datasets: [{
                        data: [
                            stats.blocked_requests || 0,
                            stats.flagged_requests || 0,
                            stats.safe_requests || 0
                        ],
                        backgroundColor: ['#f43f5e', '#fbbf24', '#10b981'],
                        borderWidth: 0
                    }]
                },
                options: {
                    responsive: true,
                    maintainAspectRatio: false,
                    plugins: { legend: { position: 'bottom' } }
                }
            });
        }

        // Timeline chart
        const timelineCtx = document.getElementById('timelineChart')?.getContext('2d');
        if (timelineCtx) {
            if (this.charts.timeline) {
                // Update existing chart with real data
                this.charts.timeline.data.datasets[0].data = timelineData;
                this.charts.timeline.update();
            } else {
                this.charts.timeline = new Chart(timelineCtx, {
                    type: 'line',
                    data: {
                        labels: ['6h ago', '5h ago', '4h ago', '3h ago', '2h ago', '1h ago', 'Now'],
                        datasets: [{
                            label: 'Threats',
                            data: timelineData,
                            borderColor: '#f43f5e',
                            tension: 0.4,
                            fill: true,
                            backgroundColor: 'rgba(244, 63, 94, 0.1)'
                        }]
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        plugins: { legend: { display: false } },
                        scales: {
                            y: { beginAtZero: true, grid: { color: '#3d424a' } },
                            x: { grid: { color: '#3d424a' } }
                        }
                    }
                });
            }
        }
    },

    // Calculate timeline data from logs (blocked requests per hour for last 6 hours)
    calculateTimelineData(logs) {
        const now = new Date();
        const hourBuckets = [0, 0, 0, 0, 0, 0, 0]; // 6h ago, 5h ago, ..., Now
        
        if (!logs || logs.length === 0) {
            return hourBuckets;
        }

        logs.forEach(log => {
            // Only count blocked requests
            if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
            
            const logTime = new Date(log.timestamp);
            const hoursAgo = Math.floor((now - logTime) / (1000 * 60 * 60));
            
            if (hoursAgo < 0) return; // Future (shouldn't happen)
            if (hoursAgo > 6) return; // Older than 6 hours
            
            // Map to bucket: 0 hours ago = index 6 (Now), 6 hours ago = index 0
            const bucketIndex = 6 - hoursAgo;
            if (bucketIndex >= 0 && bucketIndex < 7) {
                hourBuckets[bucketIndex]++;
            }
        });

        return hourBuckets;
    },

    // Calculate and display threats delta (today vs last hour)
    updateThreatsDelta(currentTotal, logs) {
        const deltaEl = document.getElementById('threats-delta');
        if (!deltaEl) return;

        // Count threats from last hour vs this hour
        const now = new Date();
        const oneHourAgo = new Date(now - 60 * 60 * 1000);
        const twoHoursAgo = new Date(now - 2 * 60 * 60 * 1000);

        let thisHour = 0;
        let lastHour = 0;

        if (logs && logs.length > 0) {
            logs.forEach(log => {
                if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
                const logTime = new Date(log.timestamp);
                if (logTime >= oneHourAgo) {
                    thisHour++;
                } else if (logTime >= twoHoursAgo) {
                    lastHour++;
                }
            });
        }

        // Calculate percentage change
        if (lastHour === 0 && thisHour === 0) {
            deltaEl.innerHTML = '<i class="fas fa-minus"></i> No recent activity';
            deltaEl.className = 'stat-delta text-muted';
        } else if (lastHour === 0) {
            deltaEl.innerHTML = `<i class="fas fa-arrow-up"></i> ${thisHour} new this hour`;
            deltaEl.className = 'stat-delta text-warning';
        } else {
            const changePercent = Math.round((thisHour - lastHour) / lastHour * 100);
            if (thisHour > lastHour) {
                deltaEl.innerHTML = `<i class="fas fa-arrow-up"></i> ${changePercent}% from last hour`;
                deltaEl.className = 'stat-delta text-danger';
            } else if (thisHour < lastHour) {
                deltaEl.innerHTML = `<i class="fas fa-arrow-down"></i> ${Math.abs(changePercent)}% from last hour`;
                deltaEl.className = 'stat-delta text-success';
            } else {
                deltaEl.innerHTML = '<i class="fas fa-equals"></i> Same as last hour';
                deltaEl.className = 'stat-delta text-muted';
            }
        }
    },

    // Fetch full logs
    async fetchLogs() {
        const logs = await this.api('/api/logs');
        const tbody = document.getElementById('full-logs-table');
        if (!tbody || !logs) return;

        if (logs.length === 0) {
            tbody.innerHTML = '<tr><td colspan="8" class="text-center text-muted py-4">No logs recorded yet. Navigate around to generate logs.</td></tr>';
            return;
        }

        // Reverse to show newest first
        const sortedLogs = [...logs].reverse();

        tbody.innerHTML = sortedLogs.map(log => {
            // Status badge
            let statusBadge = '';
            let rowClass = '';
            switch (log.status) {
                case 'Blocked':
                    statusBadge = '<span class="badge bg-danger">Blocked</span>';
                    rowClass = 'table-danger';
                    break;
                case 'ThreatBlocked':
                    statusBadge = '<span class="badge bg-danger"><i class="fas fa-skull me-1"></i>Threat</span>';
                    rowClass = 'table-danger';
                    break;
                case 'Flagged':
                    statusBadge = '<span class="badge bg-warning text-dark">Flagged</span>';
                    rowClass = 'table-warning';
                    break;
                case 'Safe':
                default:
                    statusBadge = '<span class="badge bg-success">Safe</span>';
                    break;
            }

            // Action badge - escape user data
            const safeAction = escapeHtml(log.action || 'Unknown');
            const actionBadge = log.action === 'Pass' 
                ? '<span class="badge bg-success">Pass</span>'
                : `<span class="badge bg-danger">${safeAction}</span>`;

            // Method badge color
            const methodColors = {
                'GET': 'bg-info',
                'POST': 'bg-warning text-dark',
                'PUT': 'bg-primary',
                'DELETE': 'bg-danger',
                'PATCH': 'bg-secondary'
            };
            const methodClass = methodColors[log.method] || 'bg-secondary';

            // Escape all user-controlled data
            const safeTimestamp = escapeHtml(new Date(log.timestamp).toLocaleString());
            const safeClientIP = escapeHtml(log.client_ip || '127.0.0.1');
            const safeMethod = escapeHtml(log.method || 'GET');
            const safeUri = escapeHtml(log.uri || '/');
            const safeRuleId = escapeHtml(log.rule_id || '-');
            const rawDetails = String(log.details || '');
            const safeDetails = escapeHtml(rawDetails.substring(0, 50)) + (rawDetails.length > 50 ? '...' : '');

            return `
                <tr class="${escapeAttr(rowClass)}">
                    <td class="text-secondary font-monospace small">${safeTimestamp}</td>
                    <td>${statusBadge}</td>
                    <td>${safeClientIP}</td>
                    <td><span class="badge ${escapeAttr(methodClass)}">${safeMethod}</span></td>
                    <td class="font-monospace small"><code>${safeUri}</code></td>
                    <td>${safeRuleId}</td>
                    <td class="small text-muted">${safeDetails}</td>
                    <td>${actionBadge}</td>
                </tr>
            `;
        }).join('');

        // Update pagination text
        const paginationText = document.querySelector('.card-footer .text-muted');
        if (paginationText) {
            paginationText.textContent = `Showing ${Math.min(sortedLogs.length, 50)} of ${sortedLogs.length} entries`;
        }
    },

    // Export logs to CSV
    async exportLogsCSV() {
        console.log('exportLogsCSV called');
        try {
            const logs = await this.api('/api/logs');
            console.log('Logs fetched:', logs?.length || 0);
            
            if (!logs || logs.length === 0) {
                this.showToast('Info', 'No logs to export');
                return;
            }

            // Build CSV content
            const headers = ['Timestamp', 'Status', 'Client IP', 'Method', 'URI', 'Rule ID', 'Action', 'Details'];
            const csvRows = [headers.join(',')];

            for (const log of logs) {
                const row = [
                    new Date(log.timestamp).toISOString(),
                    log.status || 'Unknown',
                    log.client_ip || '',
                    log.method || 'GET',
                    `"${(log.uri || '/').replace(/"/g, '""')}"`,
                    log.rule_id || '',
                    log.action || 'Pass',
                    `"${(log.details || '').replace(/"/g, '""')}"`
                ];
                csvRows.push(row.join(','));
            }

            const csvContent = csvRows.join('\n');
            const blob = new Blob([csvContent], { type: 'text/csv;charset=utf-8;' });
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = `obsidian-attack-logs-${new Date().toISOString().split('T')[0]}.csv`;
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);

            this.showToast('Success', `Exported ${logs.length} log entries`);
        } catch (err) {
            console.error('Export error:', err);
            this.showToast('Error', 'Failed to export logs: ' + err.message, true);
        }
    },

    // Filter logs by search term
    filterLogs() {
        const searchInput = document.getElementById('logs-search');
        const searchTerm = searchInput?.value?.toLowerCase() || '';
        const tbody = document.getElementById('full-logs-table');
        
        if (!tbody) {
            console.warn('full-logs-table not found');
            return;
        }

        const rows = tbody.querySelectorAll('tr');
        let visibleCount = 0;
        let totalCount = rows.length;

        rows.forEach(row => {
            const text = row.textContent?.toLowerCase() || '';
            if (searchTerm === '' || text.includes(searchTerm)) {
                row.style.display = '';
                visibleCount++;
            } else {
                row.style.display = 'none';
            }
        });

        // Update count
        const paginationText = document.querySelector('#view-logs .card-footer .text-muted');
        if (paginationText) {
            if (searchTerm) {
                paginationText.textContent = `Showing ${visibleCount} of ${totalCount} entries (filtered)`;
            } else {
                paginationText.textContent = `Showing ${visibleCount} of ${totalCount} entries`;
            }
        }
    },

    setRulesFilter(filter) {
        this.rulesFilter = filter || 'effective';
        const effectiveBtn = document.getElementById('rules-filter-effective');
        const customBtn = document.getElementById('rules-filter-custom');
        const crsBtn = document.getElementById('rules-filter-crs');
        [effectiveBtn, customBtn, crsBtn].forEach(btn => btn && btn.classList.remove('active'));
        if (this.rulesFilter === 'custom' && customBtn) customBtn.classList.add('active');
        else if (this.rulesFilter === 'crs' && crsBtn) crsBtn.classList.add('active');
        else if (effectiveBtn) effectiveBtn.classList.add('active');
        this.fetchRules();
    },

    // Fetch rules
    async fetchRules() {
        const qs = this.rulesFilter && this.rulesFilter !== 'effective'
            ? `?source=${encodeURIComponent(this.rulesFilter)}`
            : '';
        const rules = await this.api(`/api/rules${qs}`);
        const tbody = document.getElementById('rules-tbody');
        if (!tbody) return;
        if (!rules || rules.length === 0) {
            if (this.rulesFilter === 'crs') {
                tbody.innerHTML = `<tr><td colspan="9" class="text-center text-muted py-4">
                    <i class="fas fa-info-circle me-2"></i>
                    No OWASP CRS rules loaded. Set <code>OBSIDIAN_CRS_PATH</code> and <code>OBSIDIAN_CRS_ENABLED=true</code> to load CRS rules.
                </td></tr>`;
            } else {
                tbody.innerHTML = `<tr><td colspan="9" class="text-center text-muted py-4">No rules found.</td></tr>`;
            }
            return;
        }

        tbody.innerHTML = rules.map(rule => {
            // Determine severity badge class
            let sevClass = 'badge-sev-medium';
            if (rule.severity === 'CRITICAL') {
                sevClass = 'badge-sev-critical';
            } else if (rule.severity === 'HIGH') {
                sevClass = 'badge-sev-high';
            }
            
            // Determine status badge - use positive condition
            const isEnabled = rule.enabled !== false;
            const statusBadge = isEnabled
                ? '<span class="badge bg-success">Active</span>'
                : '<span class="badge bg-secondary">Disabled</span>';

            // Action badge with color coding
            const actionColors = {
                'deny': 'bg-danger',
                'drop': 'bg-dark',
                'log': 'bg-info',
                'pass': 'bg-success',
                'redirect': 'bg-warning text-dark'
            };
            const actionColor = actionColors[rule.action] || 'bg-secondary';
            const actionBadge = rule.action 
                ? `<span class="badge ${actionColor}">${escapeHtml(rule.action.toUpperCase())}</span>`
                : '<span class="badge bg-secondary">N/A</span>';

            // Block status display
            const blockInfo = rule.block_status && rule.block_status > 0
                ? `<code class="text-danger">${rule.block_status}</code>`
                : '<span class="text-muted">-</span>';

            // Escape user-controlled data
            const safeId = escapeHtml(rule.id);
            const safeDesc = escapeHtml(rule.description);
            const safeSeverity = escapeHtml(rule.severity || 'NOTICE');
            const safeCategory = escapeHtml(rule.category || 'General');
            const safeSource = escapeHtml((rule.source || 'custom').toUpperCase());
            const safeTarget = escapeHtml(rule.target_field || 'REQUEST_URI');
            const ruleIdNum = Number.parseInt(rule.id, 10) || 0;
            const isReadOnly = rule.read_only === true || rule.source === 'crs';

            // Match statistics
            const matchCount = rule.match_count || 0;
            const lastMatch = rule.last_match ? new Date(rule.last_match).toLocaleString() : 'Never';

            const actionsDisabled = isReadOnly ? 'disabled' : '';
            const actionsTitle = isReadOnly ? 'Read-only rule' : 'Edit Rule';

            return `
                <tr>
                    <td class="font-monospace">${safeId}</td>
                    <td>
                        <div>${safeDesc}</div>
                        <small class="text-muted">Target: <code>${safeTarget}</code></small>
                    </td>
                    <td><span class="badge ${escapeAttr(sevClass)}">${safeSeverity}</span></td>
                    <td>${safeCategory}</td>
                    <td><span class="badge bg-secondary">${safeSource}</span></td>
                    <td>${actionBadge} ${blockInfo}</td>
                    <td>${statusBadge}</td>
                    <td>
                        <small class="text-muted">${matchCount} hits</small><br>
                        <small class="text-muted">${lastMatch}</small>
                    </td>
                    <td>
                        <button class="btn btn-sm btn-outline-info me-1" onclick="ObsidianApp.editRule(${ruleIdNum})" title="${escapeAttr(actionsTitle)}" ${actionsDisabled}>
                            <i class="fas fa-edit"></i>
                        </button>
                        <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.deleteRule(${ruleIdNum})" title="Delete Rule" ${actionsDisabled}>
                            <i class="fas fa-trash"></i>
                        </button>
                    </td>
                </tr>
            `;
        }).join('');
    },

    // Fetch admin data
    async fetchAdminData() {
        const [users, audit, metrics] = await Promise.all([
            this.api('/api/admin/users'),
            this.api('/api/admin/audit'),
            this.api('/api/metrics')
        ]);

        // Users table
        const usersTbody = document.getElementById('users-tbody');
        if (usersTbody && users) {
            usersTbody.innerHTML = users.map(u => {
                const safeUsername = escapeHtml(u.username);
                const safeRole = escapeHtml(u.role);
                // Use single quotes for onclick and escape any single quotes in the data
                const usernameEsc = u.username.replace(/'/g, "\\'");
                const roleEsc = u.role.replace(/'/g, "\\'");
                return `
                <tr>
                    <td>${safeUsername}</td>
                    <td><span class="badge bg-info">${safeRole}</span></td>
                    <td>${u.enabled ? '<span class="badge bg-success">Active</span>' : '<span class="badge bg-danger">Disabled</span>'}</td>
                    <td><button class="btn btn-sm btn-outline-secondary" onclick="ObsidianApp.openUserEditModal('${usernameEsc}', '${roleEsc}', ${!!u.enabled})" title="Edit User"><i class="fas fa-edit"></i> Edit</button></td>
                </tr>
            `}).join('');
        }

        // Audit logs
        const auditList = document.getElementById('audit-list');
        if (auditList && audit && Array.isArray(audit)) {
            if (audit.length === 0) {
                auditList.innerHTML = '<li class="list-group-item bg-transparent border-secondary text-light text-center"><em>No audit logs available</em></li>';
            } else {
                auditList.innerHTML = audit.map(log => {
                    // Format timestamp - handle both 'time' and 'timestamp' fields
                    const timestamp = log.time || log.timestamp || log.created_at;
                    const formattedTime = timestamp ? new Date(timestamp).toLocaleString() : 'Unknown time';
                    
                    // Get action/message - handle various field names
                    const action = log.message || log.action || log.type || 'Activity';
                    
                    // Get resource - use request_uri, resource, or client_ip
                    const resource = log.request_uri || log.resource || log.client_ip || '';
                    
                    // Get severity badge if available
                    const severityBadge = log.severity ? 
                        `<span class="badge bg-${log.severity === 'HIGH' ? 'danger' : log.severity === 'MEDIUM' ? 'warning' : 'info'} ms-2">${escapeHtml(log.severity)}</span>` : '';
                    
                    return `
                        <li class="list-group-item bg-transparent border-secondary text-light">
                            <small class="text-muted">${escapeHtml(formattedTime)}</small>${severityBadge}<br>
                            <strong>${escapeHtml(action)}</strong>${resource ? ' on ' + escapeHtml(resource) : ''}
                        </li>
                    `;
                }).join('');
            }
        } else if (auditList) {
            auditList.innerHTML = '<li class="list-group-item bg-transparent border-secondary text-light text-center"><em>No audit logs available</em></li>';
        }

        // Metrics
        if (metrics) {
            document.getElementById('metric-memory').innerText = metrics.memory_alloc_mb ? metrics.memory_alloc_mb + ' MB' : '-';
            document.getElementById('metric-goroutines').innerText = metrics.goroutines || '-';
            document.getElementById('metric-rps').innerText = metrics.total_requests || '0';
            document.getElementById('metric-uptime').innerText = this.formatUptime(metrics.uptime_seconds);
        }
    },

    // Fetch threat intelligence
    async fetchThreatIntelligence() {
        const threats = await this.api('/api/threats');
        const tbody = document.getElementById('threats-tbody');
        if (!tbody) return;

        if (threats && threats.length > 0) {
            tbody.innerHTML = threats.map(t => {
                // Determine risk level badge color
                let riskColor = 'secondary';
                if (t.risk_level === 'HIGH') {
                    riskColor = 'danger';
                } else if (t.risk_level === 'MEDIUM') {
                    riskColor = 'warning';
                }
                
                // Escape user-controlled data
                const safeIP = escapeHtml(t.ip);
                const safeRiskLevel = escapeHtml(t.risk_level);
                const safeCategory = escapeHtml(t.category);
                const safeSource = escapeHtml(t.source);
                const safeLastSeen = escapeHtml(new Date(t.last_seen).toLocaleString());
                // Use JSON.stringify for safe JS string literal in onclick
                const ipJson = JSON.stringify(t.ip);

                return `
                    <tr>
                        <td class="font-monospace">${safeIP}</td>
                        <td><span class="badge bg-${escapeAttr(riskColor)}">${safeRiskLevel}</span></td>
                        <td>${safeCategory}</td>
                        <td>${safeSource}</td>
                        <td>${safeLastSeen}</td>
                        <td><button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.blockIP(${ipJson})" title="Block IP"><i class="fas fa-ban"></i> Block</button></td>
                    </tr>
                `;
            }).join('');
        } else {
            tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted">No threat intelligence data available</td></tr>';
        }
    },

    // Rule CRUD operations
    openRuleModal(ruleId = null) {
        const modal = new bootstrap.Modal(document.getElementById('ruleModal'));
        document.getElementById('ruleModalTitle').innerText = ruleId ? 'Edit Rule' : 'Add New Rule';
        document.getElementById('ruleEditId').value = ruleId || '';
        document.getElementById('ruleId').value = '';
        document.getElementById('ruleDesc').value = '';
        document.getElementById('ruleCategory').value = 'XSS';
        document.getElementById('ruleSeverity').value = 'CRITICAL';
        document.getElementById('ruleEnabled').checked = true;
        document.getElementById('ruleId').disabled = !!ruleId;
        // New fields
        document.getElementById('rulePattern').value = '';
        document.getElementById('ruleTargetField').value = 'REQUEST_URI';
        document.getElementById('ruleAction').value = 'deny';
        document.getElementById('ruleBlockStatus').value = '403';
        document.getElementById('ruleThreshold').value = '0';
        document.getElementById('ruleTimeWindow').value = '60';

        if (ruleId) {
            this.loadRuleForEdit(ruleId);
        }
        modal.show();
    },

    async loadRuleForEdit(ruleId) {
        const rules = await this.api('/api/rules');
        const rule = rules?.find(r => r.id === ruleId);
        if (rule) {
            document.getElementById('ruleId').value = rule.id;
            document.getElementById('ruleDesc').value = rule.description;
            document.getElementById('ruleCategory').value = rule.category || 'XSS';
            document.getElementById('ruleSeverity').value = rule.severity || 'CRITICAL';
            document.getElementById('ruleEnabled').checked = rule.enabled !== false;
            // New fields
            document.getElementById('rulePattern').value = rule.pattern || '';
            document.getElementById('ruleTargetField').value = rule.target_field || 'REQUEST_URI';
            document.getElementById('ruleAction').value = rule.action || 'deny';
            document.getElementById('ruleBlockStatus').value = String(rule.block_status || 403);
            document.getElementById('ruleThreshold').value = String(rule.threshold || 0);
            document.getElementById('ruleTimeWindow').value = String(rule.time_window || 60);
        }
    },

    async saveRule() {
        const editId = document.getElementById('ruleEditId').value;
        const rule = {
            id: Number.parseInt(document.getElementById('ruleId').value, 10),
            description: document.getElementById('ruleDesc').value,
            category: document.getElementById('ruleCategory').value,
            severity: document.getElementById('ruleSeverity').value,
            enabled: document.getElementById('ruleEnabled').checked,
            // New fields
            pattern: document.getElementById('rulePattern').value,
            target_field: document.getElementById('ruleTargetField').value,
            action: document.getElementById('ruleAction').value,
            block_status: Number.parseInt(document.getElementById('ruleBlockStatus').value, 10),
            threshold: Number.parseInt(document.getElementById('ruleThreshold').value, 10) || 0,
            time_window: Number.parseInt(document.getElementById('ruleTimeWindow').value, 10) || 60
        };

        const endpoint = editId ? '/api/rules/update' : '/api/rules/create';
        const method = editId ? 'PUT' : 'POST';

        const data = await this.api(endpoint, { method, body: JSON.stringify(rule) });
        if (data?.success) {
            this.showToast('Success', editId ? 'Rule updated' : 'Rule created');
            bootstrap.Modal.getInstance(document.getElementById('ruleModal')).hide();
            this.fetchRules();
        } else {
            this.showToast('Error', data?.message || 'Failed to save rule', true);
        }
    },

    editRule(ruleId) {
        this.openRuleModal(ruleId);
    },

    async deleteRule(ruleId) {
        if (!confirm('Delete rule ' + ruleId + '?')) return;

        const data = await this.api('/api/rules/delete', {
            method: 'DELETE',
            body: JSON.stringify({ id: ruleId })
        });

        if (data?.success) {
            this.showToast('Success', 'Rule deleted');
            this.fetchRules();
        } else {
            this.showToast('Error', data?.message || 'Failed to delete', true);
        }
    },

    async blockIP(ip) {
        if (!confirm(`Block IP ${ip}?`)) return;
        const data = await this.api('/api/threats/block', {
            method: 'POST',
            body: JSON.stringify({ ip })
        });
        this.showToast(data?.success ? 'Success' : 'Error', data?.message || 'IP blocked');
    },

    // ============================================
    // USER MANAGEMENT
    // ============================================
    openUserEditModal(username, role, enabled) {
        const modal = new bootstrap.Modal(document.getElementById('userEditModal'));
        document.getElementById('editUserUsername').value = username;
        document.getElementById('editUserRole').value = role;
        document.getElementById('editUserEnabled').checked = enabled;
        document.getElementById('editUserPassword').value = '';
        modal.show();
    },

    async saveUserEdit() {
        const username = document.getElementById('editUserUsername').value;
        const role = document.getElementById('editUserRole').value;
        const enabled = document.getElementById('editUserEnabled').checked;
        const password = document.getElementById('editUserPassword').value;

        const payload = { username, role, enabled };
        if (password && password.trim() !== '') {
            payload.password = password;
        }

        const data = await this.api('/api/admin/users', {
            method: 'PUT',
            body: JSON.stringify(payload)
        });

        if (data?.success) {
            this.showToast('Success', 'User updated successfully');
            bootstrap.Modal.getInstance(document.getElementById('userEditModal')).hide();
            this.fetchAdminData();
        } else {
            this.showToast('Error', data?.message || 'Failed to update user', true);
        }
    },

    // ============================================
    // GEOIP MANAGEMENT
    // ============================================
    async fetchGeoIPData() {
        const [metrics, blocked] = await Promise.all([
            this.api('/api/geoip/metrics'),
            this.api('/api/geoip/blocked')
        ]);

        if (metrics) {
            // API returns total_lookups, cache_hits, cache_misses
            // blocked_countries comes from the blocked API response
            const blockedCount = blocked?.blocked_countries || blocked?.countries?.length || 0;
            document.getElementById('geoip-blocked-count').innerText = blockedCount;
            document.getElementById('geoip-blocked-today').innerText = metrics.total_lookups || 0;
            document.getElementById('geoip-status').innerText = 'Active';
            
            // Render top countries from blocked data
            const topCountries = document.getElementById('geoip-top-countries');
            const countries = blocked?.countries || [];
            if (topCountries && countries.length > 0) {
                topCountries.innerHTML = countries.slice(0, 5).map(c => `
                    <div class="d-flex justify-content-between align-items-center mb-2">
                        <span><i class="fas fa-flag me-2"></i>${escapeHtml(c.name || c.code)}</span>
                        <span class="badge bg-danger">${escapeHtml(c.blocked_count || 0)} blocked</span>
                    </div>
                `).join('');
            } else {
                topCountries.innerHTML = '<div class="text-muted text-center">No blocked countries</div>';
            }
        }

        const tbody = document.getElementById('geoip-blocked-tbody');
        if (tbody && blocked) {
            // API returns {countries: []} with code/name fields
            const countries = blocked.countries || blocked || [];
            if (countries.length > 0) {
                tbody.innerHTML = countries.map(c => `
                    <tr>
                        <td>${escapeHtml(c.name || c.country_name || c.code)}</td>
                        <td class="font-monospace">${escapeHtml(c.code || c.country_code)}</td>
                        <td>${escapeHtml(c.blocked_count || 0)}</td>
                        <td>
                            <button class="btn btn-sm btn-outline-success" onclick="ObsidianApp.unblockCountry('${escapeAttr(c.code || c.country_code)}')" title="Unblock">
                                <i class="fas fa-unlock"></i>
                            </button>
                        </td>
                    </tr>
                `).join('');
            } else {
                tbody.innerHTML = '<tr><td colspan="4" class="text-center text-muted py-4">No countries blocked</td></tr>';
            }
        }
    },

    openGeoIPModal() {
        const modal = new bootstrap.Modal(document.getElementById('geoipModal'));
        document.getElementById('geoipCountryCode').value = '';
        document.getElementById('geoipReason').value = '';
        modal.show();
    },

    async blockCountry() {
        const code = document.getElementById('geoipCountryCode').value.toUpperCase();
        const reason = document.getElementById('geoipReason').value;
        
        if (!code || code.length !== 2) {
            this.showToast('Error', 'Please enter a valid 2-letter country code', true);
            return;
        }

        const data = await this.api('/api/geoip/blocked', {
            method: 'POST',
            body: JSON.stringify({ country_code: code, reason })
        });

        if (data?.success) {
            this.showToast('Success', `Country ${code} blocked`);
            bootstrap.Modal.getInstance(document.getElementById('geoipModal')).hide();
            this.fetchGeoIPData();
        } else {
            this.showToast('Error', data?.message || 'Failed to block country', true);
        }
    },

    async unblockCountry(code) {
        if (!confirm(`Unblock country ${code}?`)) return;
        
        const data = await this.api(`/api/geoip/blocked?code=${encodeURIComponent(code)}`, {
            method: 'DELETE'
        });

        if (data?.success) {
            this.showToast('Success', `Country ${code} unblocked`);
            this.fetchGeoIPData();
        } else {
            this.showToast('Error', data?.message || 'Failed to unblock', true);
        }
    },

    async lookupIP() {
        const ip = document.getElementById('geoip-lookup-ip').value;
        if (!ip) {
            this.showToast('Error', 'Please enter an IP address', true);
            return;
        }

        const data = await this.api(`/api/geoip/lookup?ip=${encodeURIComponent(ip)}`);
        const resultDiv = document.getElementById('geoip-lookup-result');
        
        if (data && !data.error) {
            // Build location string with city if available
            let locationStr = data.country_name || 'Unknown';
            if (data.city && data.city !== '') {
                locationStr = `${data.city}, ${data.country_name}`;
            }
            document.getElementById('lookup-country').innerText = locationStr;
            document.getElementById('lookup-code').innerText = data.country_code || '-';
            document.getElementById('lookup-blocked').innerHTML = data.is_blocked 
                ? '<span class="badge bg-danger">Yes</span>' 
                : '<span class="badge bg-success">No</span>';
            
            // Show risk level with threat score
            const riskLevel = data.risk_level || 'low';
            const riskColor = riskLevel === 'high' ? 'danger' : (riskLevel === 'medium' ? 'warning' : 'success');
            document.getElementById('lookup-risk').innerHTML = 
                `<span class="badge bg-${riskColor}">${riskLevel.toUpperCase()} (${data.threat_score || 0})</span>`;
            
            // Show additional info if ISP/org available
            if (data.isp || data.organization) {
                const ispInfo = data.isp || data.organization || '';
                document.getElementById('lookup-code').innerText = `${data.country_code || '-'} | ${ispInfo}`;
            }
            
            resultDiv.classList.remove('d-none');
        } else {
            this.showToast('Error', data?.error || 'IP lookup failed', true);
            resultDiv.classList.add('d-none');
        }
    },

    // ============================================
    // RATE LIMITING MANAGEMENT
    // ============================================
    async fetchRateLimitData() {
        const data = await this.api('/api/metrics');
        
        if (data?.rate_limiter) {
            const rl = data.rate_limiter;
            document.getElementById('ratelimit-limited-count').innerText = rl.rate_limited_ips || 0;
            document.getElementById('ratelimit-blacklist-count').innerText = rl.blacklisted_count || 0;
            document.getElementById('ratelimit-whitelist-count').innerText = rl.whitelisted_count || 0;
            document.getElementById('ratelimit-limit').innerText = rl.requests_per_minute || 200;

            // Render blacklist
            const blacklistTbody = document.getElementById('blacklist-tbody');
            if (blacklistTbody && rl.blacklist) {
                if (rl.blacklist.length > 0) {
                    blacklistTbody.innerHTML = rl.blacklist.map(ip => `
                        <tr>
                            <td class="font-monospace">${escapeHtml(ip)}</td>
                            <td class="small text-muted">-</td>
                            <td>
                                <button class="btn btn-sm btn-outline-success" onclick="ObsidianApp.removeFromBlacklist('${escapeAttr(ip)}')" title="Remove">
                                    <i class="fas fa-times"></i>
                                </button>
                            </td>
                        </tr>
                    `).join('');
                } else {
                    blacklistTbody.innerHTML = '<tr><td colspan="3" class="text-center text-muted">No blacklisted IPs</td></tr>';
                }
            }

            // Render whitelist
            const whitelistTbody = document.getElementById('whitelist-tbody');
            if (whitelistTbody && rl.whitelist) {
                if (rl.whitelist.length > 0) {
                    whitelistTbody.innerHTML = rl.whitelist.map(ip => `
                        <tr>
                            <td class="font-monospace">${escapeHtml(ip)}</td>
                            <td class="small text-muted">-</td>
                            <td>
                                <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.removeFromWhitelist('${escapeAttr(ip)}')" title="Remove">
                                    <i class="fas fa-times"></i>
                                </button>
                            </td>
                        </tr>
                    `).join('');
                } else {
                    whitelistTbody.innerHTML = '<tr><td colspan="3" class="text-center text-muted">No whitelisted IPs</td></tr>';
                }
            }
        }
    },

    async blacklistIP() {
        const ip = document.getElementById('blacklist-ip-input').value;
        if (!ip) {
            this.showToast('Error', 'Please enter an IP address', true);
            return;
        }

        const data = await this.api('/api/ratelimit/blacklist', {
            method: 'POST',
            body: JSON.stringify({ ip })
        });

        if (data?.success) {
            document.getElementById('blacklist-ip-input').value = '';
            this.showToast('Success', `IP ${ip} blacklisted`);
            this.fetchRateLimitData();
        } else {
            this.showToast('Error', data?.message || 'Failed to blacklist IP', true);
        }
    },

    async whitelistIP() {
        const ip = document.getElementById('whitelist-ip-input').value;
        if (!ip) {
            this.showToast('Error', 'Please enter an IP address', true);
            return;
        }

        const data = await this.api('/api/ratelimit/whitelist', {
            method: 'POST',
            body: JSON.stringify({ ip })
        });

        if (data?.success) {
            document.getElementById('whitelist-ip-input').value = '';
            this.showToast('Success', `IP ${ip} whitelisted`);
            this.fetchRateLimitData();
        } else {
            this.showToast('Error', data?.message || 'Failed to whitelist IP', true);
        }
    },

    async removeFromBlacklist(ip) {
        const data = await this.api('/api/ratelimit/blacklist', {
            method: 'DELETE',
            body: JSON.stringify({ ip })
        });
        if (data?.success) {
            this.showToast('Success', `IP ${ip} removed from blacklist`);
            this.fetchRateLimitData();
        }
    },

    async removeFromWhitelist(ip) {
        const data = await this.api('/api/ratelimit/whitelist', {
            method: 'DELETE',
            body: JSON.stringify({ ip })
        });
        if (data?.success) {
            this.showToast('Success', `IP ${ip} removed from whitelist`);
            this.fetchRateLimitData();
        }
    },

    // ============================================
    // ALERT WEBHOOKS MANAGEMENT
    // ============================================
    async fetchAlertsData() {
        const rawData = await this.api('/api/alerts/webhooks');
        
        // API returns array directly, normalize to object structure
        const webhooks = Array.isArray(rawData) ? rawData : (rawData?.webhooks || rawData || []);
        
        if (webhooks) {
            const enabledWebhooks = webhooks.filter(w => w.enabled);
            document.getElementById('webhooks-active-count').innerText = enabledWebhooks.length || 0;
            document.getElementById('alerts-sent-today').innerText = rawData?.alerts_sent_today || 0;
            document.getElementById('alerts-failed').innerText = rawData?.alerts_failed || 0;

            // Update webhook select dropdown for testing
            const select = document.getElementById('test-webhook-select');
            if (select) {
                select.innerHTML = '<option value="">Select webhook...</option>' + 
                    webhooks.map(w => `<option value="${escapeAttr(w.name)}">${escapeHtml(w.name)} (${escapeHtml(w.type || 'generic')})</option>`).join('');
            }

            // Render webhooks table
            const tbody = document.getElementById('webhooks-tbody');
            if (tbody) {
                if (webhooks.length > 0) {
                    tbody.innerHTML = webhooks.map(w => {
                        const typeIcons = {
                            slack: '<i class="fab fa-slack text-info"></i>',
                            teams: '<i class="fab fa-microsoft text-primary"></i>',
                            discord: '<i class="fab fa-discord text-info"></i>',
                            pagerduty: '<i class="fas fa-pager text-success"></i>',
                            generic: '<i class="fas fa-globe text-secondary"></i>'
                        };
                        const urlDisplay = w.url || '***';
                        const truncatedUrl = urlDisplay.length > 40 ? urlDisplay.substring(0, 40) + '...' : urlDisplay;
                        
                        return `
                            <tr>
                                <td>${escapeHtml(w.name)}</td>
                                <td>${typeIcons[w.type] || '<i class="fas fa-globe text-secondary"></i>'} ${escapeHtml(w.type || 'generic')}</td>
                                <td class="font-monospace small">${escapeHtml(truncatedUrl)}</td>
                                <td><span class="badge bg-${w.min_severity === 'critical' ? 'danger' : w.min_severity === 'error' ? 'warning' : 'info'}">${escapeHtml(w.min_severity || 'all')}</span></td>
                                <td>${w.enabled ? '<span class="badge bg-success">Active</span>' : '<span class="badge bg-secondary">Disabled</span>'}</td>
                                <td>
                                    <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.deleteWebhook('${escapeAttr(w.name)}')" title="Delete">
                                        <i class="fas fa-trash"></i>
                                    </button>
                                </td>
                            </tr>
                        `;
                    }).join('');
                } else {
                    tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted py-4">No webhooks configured</td></tr>';
                }
            }
        }
    },

    openWebhookModal() {
        const modal = new bootstrap.Modal(document.getElementById('webhookModal'));
        document.getElementById('webhookName').value = '';
        document.getElementById('webhookType').value = 'slack';
        document.getElementById('webhookUrl').value = '';
        document.getElementById('webhookSeverity').value = 'error';
        document.getElementById('webhookEnabled').checked = true;
        modal.show();
    },

    async saveWebhook() {
        const webhook = {
            name: document.getElementById('webhookName').value,
            type: document.getElementById('webhookType').value,
            url: document.getElementById('webhookUrl').value,
            severity: document.getElementById('webhookSeverity').value,
            enabled: document.getElementById('webhookEnabled').checked
        };

        if (!webhook.name || !webhook.url) {
            this.showToast('Error', 'Name and URL are required', true);
            return;
        }

        const data = await this.api('/api/alerts/webhooks', {
            method: 'POST',
            body: JSON.stringify(webhook)
        });

        if (data?.success) {
            this.showToast('Success', 'Webhook added');
            bootstrap.Modal.getInstance(document.getElementById('webhookModal')).hide();
            this.fetchAlertsData();
        } else {
            this.showToast('Error', data?.message || 'Failed to add webhook', true);
        }
    },

    async deleteWebhook(name) {
        if (!confirm('Delete this webhook?')) return;
        
        const data = await this.api(`/api/alerts/webhooks?name=${encodeURIComponent(name)}`, {
            method: 'DELETE'
        });

        if (data?.success) {
            this.showToast('Success', 'Webhook deleted');
            this.fetchAlertsData();
        } else {
            this.showToast('Error', data?.message || 'Failed to delete webhook', true);
        }
    },

    async testWebhook() {
        const name = document.getElementById('test-webhook-select').value;
        if (!name) {
            this.showToast('Error', 'Please select a webhook', true);
            return;
        }

        const data = await this.api(`/api/alerts/webhooks/test?name=${encodeURIComponent(name)}`, {
            method: 'POST'
        });

        if (data?.success) {
            this.showToast('Success', 'Test alert sent!');
        } else {
            this.showToast('Error', data?.message || 'Test failed', true);
        }
    },

    // WebSocket connection
    connectWebSocket() {
        const isSecure = globalThis.location.protocol === 'https:';
        const protocol = isSecure ? 'wss:' : 'ws:';
        this.ws = new WebSocket(`${protocol}//${globalThis.location.host}/api/ws?token=${this.token}`);

        this.ws.onmessage = (event) => {
            try {
                const msg = JSON.parse(event.data);
                if (msg.type === 'stats_update' && this.currentView === 'dashboard') {
                    this.fetchDashboardData();
                } else if (msg.type === 'alert') {
                    this.showToast('⚠️ Attack Blocked!', msg.data?.details || 'Security Event Detected', true);
                }
            } catch (parseError) {
                console.error('WebSocket message parse error:', parseError.message);
            }
        };

        this.ws.onclose = () => {
            setTimeout(() => this.connectWebSocket(), 2000);
        };
    },

    // Auto refresh
    startAutoRefresh() {
        this.refreshInterval = setInterval(() => {
            if (this.currentView === 'dashboard') {
                this.fetchDashboardData();
            }
        }, 30000);
    },

    // Setup event listeners
    setupEventListeners() {
        // Search filter
        document.getElementById('logs-search')?.addEventListener('keyup', function () {
            const filter = this.value.toLowerCase();
            document.querySelectorAll('#full-logs-table tr').forEach(row => {
                row.style.display = row.innerText.toLowerCase().includes(filter) ? '' : 'none';
            });
        });

        // Export button
        document.getElementById('downloadReport')?.addEventListener('click', (e) => {
            e.preventDefault();
            this.exportReport();
        });

        // Mobile toggle
        const sidebar = document.getElementById('sidebar');
        const overlay = document.querySelector('.overlay');
        const sidebarClose = document.getElementById('sidebarClose');
        
        document.querySelector('.mobile-toggle')?.addEventListener('click', () => {
            sidebar?.classList.toggle('active');
            overlay?.classList.toggle('active');
        });

        sidebarClose?.addEventListener('click', () => {
            sidebar?.classList.remove('active');
            overlay?.classList.remove('active');
        });

        // Close sidebar when clicking overlay
        overlay?.addEventListener('click', () => {
            sidebar?.classList.remove('active');
            overlay?.classList.remove('active');
        });

        // Close sidebar when clicking a nav link on mobile
        document.querySelectorAll('.nav-link').forEach(link => {
            link.addEventListener('click', () => {
                if (window.innerWidth <= 992) {
                    sidebar?.classList.remove('active');
                    overlay?.classList.remove('active');
                }
            });
        });
    },

    // Export report
    async exportReport(format = 'pdf') {
        console.log('exportReport called, format:', format);
        try {
            if (format === 'csv') {
                // Export logs as CSV
                const logs = await this.api('/api/logs');
                if (!logs || !logs.length) {
                    this.showToast('Warning', 'No logs to export', true);
                    return;
                }
                
                let csv = 'Timestamp,Client IP,Method,Path,Rule ID,Action,Status,Details\n';
                logs.forEach(log => {
                    csv += `"${log.timestamp || ''}","${log.client_ip || ''}","${log.method || ''}","${log.uri || ''}","${log.rule_id || ''}","${log.action || ''}","${log.status || ''}","${(log.details || '').replace(/"/g, '""')}"\n`;
                });
                
                const blob = new Blob([csv], { type: 'text/csv' });
                const url = URL.createObjectURL(blob);
                const a = document.createElement('a');
                a.href = url;
                a.download = 'obsidian-logs.csv';
                document.body.appendChild(a);
                a.click();
                document.body.removeChild(a);
                URL.revokeObjectURL(url);
                this.showToast('Success', 'CSV exported successfully');
                return;
            }
            
            // PDF export
            const res = await fetch('/api/export?format=pdf', {
                headers: { 'Authorization': 'Bearer ' + this.token }
            });
            
            console.log('Export response status:', res.status);
            
            if (!res.ok) {
                throw new Error('Export failed with status ' + res.status);
            }
            
            const blob = await res.blob();
            console.log('Blob size:', blob.size);
            
            const url = URL.createObjectURL(blob);
            const a = document.createElement('a');
            a.href = url;
            a.download = 'obsidian-security-report.pdf';
            document.body.appendChild(a);
            a.click();
            document.body.removeChild(a);
            URL.revokeObjectURL(url);
            this.showToast('Success', 'Report exported successfully');
        } catch (exportError) {
            console.error('Export failed:', exportError);
            this.showToast('Error', 'Failed to export report: ' + exportError.message, true);
        }
    },

    // Toast notification
    showToast(title, message, isError = false) {
        const container = document.getElementById('toast-container');
        const id = 'toast-' + Date.now();
        const color = isError ? 'text-bg-danger' : 'text-bg-dark border-secondary text-white';

        // Escape user-controlled content to prevent XSS
        const safeTitle = escapeHtml(title);
        const safeMessage = escapeHtml(message);
        const safeId = escapeAttr(id);

        container.innerHTML += `
            <div id="${safeId}" class="toast align-items-center ${escapeAttr(color)} border-0 mb-2 show" role="alert">
                <div class="d-flex">
                    <div class="toast-body"><strong>${safeTitle}</strong><br>${safeMessage}</div>
                    <button type="button" class="btn-close btn-close-white me-2 m-auto" onclick="this.parentElement.parentElement.remove()"></button>
                </div>
            </div>
        `;

        setTimeout(() => document.getElementById(id)?.remove(), 5000);
    },

    // Format uptime
    formatUptime(seconds) {
        if (!seconds) return '-';
        const h = Math.floor(seconds / 3600);
        const m = Math.floor((seconds % 3600) / 60);
        return `${h}h ${m}m`;
    },

    // ============================================
    // ANALYTICS FUNCTIONS
    // ============================================
    
    analyticsPeriod: 'today',
    
    setAnalyticsPeriod(period) {
        this.analyticsPeriod = period;
        document.querySelectorAll('#view-analytics .btn-group .btn').forEach(btn => {
            btn.classList.remove('active');
            if (btn.textContent.toLowerCase().includes(period.substring(0, 4))) {
                btn.classList.add('active');
            }
        });
        // Update the stat card labels to reflect the period
        const periodLabel = period === 'today' ? 'Today' : period === 'week' ? '7 Days' : '30 Days';
        const totalLabel = document.getElementById('analytics-total-label');
        const ipsLabel = document.getElementById('analytics-ips-label');
        if (totalLabel) totalLabel.textContent = 'Total Requests (' + periodLabel + ')';
        if (ipsLabel) ipsLabel.textContent = 'Unique IPs (' + periodLabel + ')';
        this.fetchAnalyticsData();
    },
    
    async fetchAnalyticsData() {
        const [stats, logs, metrics] = await Promise.all([
            this.api('/api/stats'),
            this.api('/api/logs'),
            this.api('/api/metrics')
        ]);

        // Filter logs by selected period
        const now = new Date();
        let filteredLogs = logs || [];
        if (filteredLogs.length > 0) {
            const cutoff = new Date(now);
            switch (this.analyticsPeriod) {
                case 'today':
                    cutoff.setHours(0, 0, 0, 0);
                    break;
                case 'week':
                    cutoff.setDate(cutoff.getDate() - 7);
                    break;
                case 'month':
                    cutoff.setDate(cutoff.getDate() - 30);
                    break;
            }
            filteredLogs = filteredLogs.filter(l => new Date(l.timestamp) >= cutoff);
        }
        
        if (stats) {
            // Compute period-specific stats from filtered logs
            const periodTotal = filteredLogs.length;
            const periodBlocked = filteredLogs.filter(l => l.status === 'Blocked' || l.status === 'ThreatBlocked').length;
            const blockRate = periodTotal > 0 ? ((periodBlocked / periodTotal) * 100).toFixed(1) : '0';
            
            document.getElementById('analytics-total-24h').textContent = periodTotal.toLocaleString();
            document.getElementById('analytics-block-rate').textContent = blockRate + '%';

            // Compute avg response time from log entries (real data)
            const logsWithRT = filteredLogs.filter(l => l.response_time_ms > 0);
            let avgRT = 0;
            if (logsWithRT.length > 0) {
                avgRT = logsWithRT.reduce((sum, l) => sum + l.response_time_ms, 0) / logsWithRT.length;
            } else if (stats.avg_response_time_ms > 0) {
                avgRT = stats.avg_response_time_ms;
            }
            document.getElementById('analytics-avg-response').textContent = avgRT.toFixed(1) + 'ms';
        }
        
        if (filteredLogs.length > 0) {
            // Calculate unique IPs from filtered logs
            const uniqueIPs = new Set(filteredLogs.map(l => l.client_ip).filter(Boolean));
            document.getElementById('analytics-unique-ips').textContent = uniqueIPs.size;
            
            // Top attacked endpoints
            const endpointStats = {};
            filteredLogs.forEach(log => {
                const uri = log.uri || '/';
                if (!endpointStats[uri]) {
                    endpointStats[uri] = { total: 0, blocked: 0 };
                }
                endpointStats[uri].total++;
                if (log.status === 'Blocked' || log.status === 'ThreatBlocked') {
                    endpointStats[uri].blocked++;
                }
            });
            
            const topEndpoints = Object.entries(endpointStats)
                .filter(([_, stats]) => stats.blocked > 0)
                .sort((a, b) => b[1].blocked - a[1].blocked)
                .slice(0, 5);
            
            const endpointsTbody = document.getElementById('analytics-top-endpoints');
            if (endpointsTbody) {
                if (topEndpoints.length === 0) {
                    endpointsTbody.innerHTML = '<tr><td colspan="3" class="text-center text-muted">No attacks detected</td></tr>';
                } else {
                    endpointsTbody.innerHTML = topEndpoints.map(([uri, stats]) => {
                        const rate = stats.total > 0 ? ((stats.blocked / stats.total) * 100).toFixed(0) : 0;
                        return `<tr>
                            <td><code class="text-info">${escapeHtml(uri.substring(0, 40))}${uri.length > 40 ? '...' : ''}</code></td>
                            <td><span class="badge bg-danger">${stats.blocked}</span></td>
                            <td>${rate}%</td>
                        </tr>`;
                    }).join('');
                }
            }
            
            // Top attackers
            const attackerStats = {};
            filteredLogs.forEach(log => {
                if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
                const ip = log.client_ip || 'Unknown';
                if (!attackerStats[ip]) {
                    attackerStats[ip] = { count: 0, country: log.country || 'Unknown' };
                }
                attackerStats[ip].count++;
            });
            
            const topAttackers = Object.entries(attackerStats)
                .sort((a, b) => b[1].count - a[1].count)
                .slice(0, 5);
            
            const attackersTbody = document.getElementById('analytics-top-attackers');
            if (attackersTbody) {
                if (topAttackers.length === 0) {
                    attackersTbody.innerHTML = '<tr><td colspan="4" class="text-center text-muted">No attackers detected</td></tr>';
                } else {
                    attackersTbody.innerHTML = topAttackers.map(([ip, stats]) => `<tr>
                        <td><code>${escapeHtml(ip)}</code></td>
                        <td>${escapeHtml(stats.country)}</td>
                        <td><span class="badge bg-danger">${stats.count}</span></td>
                        <td><button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.blacklistIP('${escapeAttr(ip)}')"><i class="fas fa-ban"></i></button></td>
                    </tr>`).join('');
                }
            }
            
            // Update charts
            this.updateAnalyticsCharts(filteredLogs);
        } else {
            document.getElementById('analytics-unique-ips').textContent = '0';
        }
    },
    
    updateAnalyticsCharts(logs) {
        // Traffic chart (7 days)
        const trafficCtx = document.getElementById('trafficChart')?.getContext('2d');
        if (trafficCtx) {
            const days = [];
            const blocked = [];
            const safe = [];
            
            for (let i = 6; i >= 0; i--) {
                const date = new Date();
                date.setDate(date.getDate() - i);
                days.push(date.toLocaleDateString('en-US', { weekday: 'short' }));
                
                const dayLogs = logs.filter(l => {
                    const logDate = new Date(l.timestamp);
                    return logDate.toDateString() === date.toDateString();
                });
                
                blocked.push(dayLogs.filter(l => l.status === 'Blocked' || l.status === 'ThreatBlocked').length);
                safe.push(dayLogs.filter(l => l.status === 'Safe').length);
            }
            
            if (this.charts.traffic) {
                this.charts.traffic.data.labels = days;
                this.charts.traffic.data.datasets[0].data = safe;
                this.charts.traffic.data.datasets[1].data = blocked;
                this.charts.traffic.update();
            } else {
                this.charts.traffic = new Chart(trafficCtx, {
                    type: 'line',
                    data: {
                        labels: days,
                        datasets: [
                            { label: 'Safe', data: safe, borderColor: '#10b981', backgroundColor: 'rgba(16, 185, 129, 0.1)', fill: true, tension: 0.4 },
                            { label: 'Blocked', data: blocked, borderColor: '#f43f5e', backgroundColor: 'rgba(244, 63, 94, 0.1)', fill: true, tension: 0.4 }
                        ]
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        scales: {
                            y: { beginAtZero: true, grid: { color: '#3d424a' } },
                            x: { grid: { color: '#3d424a' } }
                        }
                    }
                });
            }
        }
        
        // Category chart
        const categoryCtx = document.getElementById('categoryChart')?.getContext('2d');
        if (categoryCtx) {
            const categories = { 'XSS': 0, 'SQLi': 0, 'RCE': 0, 'LFI': 0, 'RFI': 0, 'Scanner': 0, 'Protocol': 0, 'Other': 0 };

            // classifyAttack uses the OWASP CRS rule ID ranges to determine
            // the attack category. Falls back to keyword matching in details.
            function classifyAttack(ruleId, details) {
                const id = ruleId || 0;
                const d = (details || '').toLowerCase();
                // Skip CRS anomaly evaluation/setup rules (900100-900999, 949110, 959100, 980xxx)
                // These are meta-rules; the detail text may have the real category
                if ((id >= 900100 && id < 901000) || (id >= 949000 && id < 950000) || (id >= 959000 && id < 960000) || (id >= 980000 && id < 990000)) {
                    // Try to classify from the details text instead
                    if (d.includes('xss') || d.includes('cross-site') || d.includes('script')) return 'XSS';
                    if (d.includes('sql injection') || d.includes('sqli')) return 'SQLi';
                    if (d.includes('remote code') || d.includes('rce') || d.includes('command')) return 'RCE';
                    if (d.includes('local file') || d.includes('lfi') || d.includes('traversal')) return 'LFI';
                    if (d.includes('remote file') || d.includes('rfi')) return 'RFI';
                    if (d.includes('scanner') || d.includes('bot')) return 'Scanner';
                    if (d.includes('protocol')) return 'Protocol';
                    return 'Other';
                }
                // CRS / custom rule ID ranges (OWASP convention)
                if (id >= 941000 && id < 942000) return 'XSS';
                if (id >= 942000 && id < 943000) return 'SQLi';
                if (id >= 932000 && id < 933000) return 'RCE';
                if (id >= 930000 && id < 931000) return 'LFI';
                if (id >= 931000 && id < 932000) return 'RFI';
                if (id >= 913000 && id < 914000) return 'Scanner';
                if (id >= 920000 && id < 921000) return 'Protocol';
                if (id >= 933000 && id < 935000) return 'RCE';   // PHP/Node injection = code exec
                if (id >= 943000 && id < 945000) return 'SQLi';  // Session fixation / Java
                if (id >= 910000 && id < 913000) return 'Protocol'; // method/protocol rules
                if (id === 900001) return 'Other'; // test rule
                // Keyword fallback for threat-intel or custom entries
                if (d.includes('xss') || d.includes('cross-site') || d.includes('script')) return 'XSS';
                if (d.includes('sqli') || d.includes('sql') || d.includes('injection')) return 'SQLi';
                if (d.includes('rce') || d.includes('command') || d.includes('exec')) return 'RCE';
                if (d.includes('lfi') || d.includes('traversal') || d.includes('path')) return 'LFI';
                if (d.includes('rfi') || d.includes('remote file')) return 'RFI';
                if (d.includes('scanner') || d.includes('bot') || d.includes('crawler')) return 'Scanner';
                return 'Other';
            }

            logs.forEach(log => {
                if (log.status !== 'Blocked' && log.status !== 'ThreatBlocked') return;
                const cat = classifyAttack(log.rule_id, log.details);
                categories[cat] = (categories[cat] || 0) + 1;
            });
            
            if (this.charts.category) {
                this.charts.category.data.datasets[0].data = Object.values(categories);
                this.charts.category.update();
            } else {
                this.charts.category = new Chart(categoryCtx, {
                    type: 'doughnut',
                    data: {
                        labels: Object.keys(categories),
                        datasets: [{
                            data: Object.values(categories),
                            backgroundColor: ['#8b5cf6', '#f59e0b', '#ef4444', '#06b6d4', '#14b8a6', '#f97316', '#a78bfa', '#6b7280']
                        }]
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        plugins: { legend: { position: 'right' } }
                    }
                });
            }
        }
        
        // Hourly chart
        const hourlyCtx = document.getElementById('hourlyChart')?.getContext('2d');
        if (hourlyCtx) {
            const hourlyData = Array(24).fill(0);
            const today = new Date().toDateString();
            logs.forEach(log => {
                const logDate = new Date(log.timestamp);
                if (logDate.toDateString() === today) {
                    hourlyData[logDate.getHours()]++;
                }
            });
            
            const labels = Array.from({ length: 24 }, (_, i) => `${i}:00`);
            
            if (this.charts.hourly) {
                this.charts.hourly.data.datasets[0].data = hourlyData;
                this.charts.hourly.update();
            } else {
                this.charts.hourly = new Chart(hourlyCtx, {
                    type: 'bar',
                    data: {
                        labels,
                        datasets: [{
                            label: 'Requests',
                            data: hourlyData,
                            backgroundColor: 'rgba(59, 130, 246, 0.5)',
                            borderColor: '#3b82f6',
                            borderWidth: 1
                        }]
                    },
                    options: {
                        responsive: true,
                        maintainAspectRatio: false,
                        scales: {
                            y: { beginAtZero: true, grid: { color: '#3d424a' } },
                            x: { grid: { display: false } }
                        }
                    }
                });
            }
        }
    },

    // ============================================
    // SYSTEM HEALTH FUNCTIONS
    // ============================================
    
    async fetchHealthData() {
        const [health, metrics] = await Promise.all([
            this.api('/api/health'),
            this.api('/api/metrics')
        ]);
        
        if (health) {
            // System status
            const statusEl = document.getElementById('health-status');
            if (statusEl) {
                if (health.status === 'healthy') {
                    statusEl.innerHTML = '<span class="badge bg-success fs-5"><i class="fas fa-check-circle me-1"></i>Healthy</span>';
                } else {
                    statusEl.innerHTML = '<span class="badge bg-danger fs-5"><i class="fas fa-exclamation-circle me-1"></i>Degraded</span>';
                }
            }
            
            // Parse databases object from health response
            const databases = health.databases || {};
            
            // PostgreSQL status
            const postgresEl = document.getElementById('health-postgres');
            if (postgresEl) {
                const pgStatus = databases.postgres || 'not_configured';
                const isConnected = pgStatus === 'connected';
                postgresEl.textContent = isConnected ? 'Connected' : (pgStatus === 'not_configured' ? 'Not Configured' : 'Disconnected');
                postgresEl.className = `badge bg-${isConnected ? 'success' : (pgStatus === 'not_configured' ? 'secondary' : 'danger')}`;
            }
            
            // Redis status
            const redisEl = document.getElementById('health-redis');
            if (redisEl) {
                const redisStatus = databases.redis || 'not_configured';
                const isConnected = redisStatus === 'connected';
                redisEl.textContent = isConnected ? 'Connected' : 'Not Configured';
                redisEl.className = `badge bg-${isConnected ? 'success' : 'secondary'}`;
            }
            
            // GeoIP status from features
            const geoipEl = document.getElementById('health-geoip');
            if (geoipEl && health.features) {
                const geoipActive = health.features.geoip;
                geoipEl.textContent = geoipActive ? 'Active' : 'Not Configured';
                geoipEl.className = `badge bg-${geoipActive ? 'success' : 'secondary'}`;
            }
            
            // Alert service from features
            const alertsEl = document.getElementById('health-alerts');
            if (alertsEl && health.features) {
                const alertsActive = health.features.alerts;
                alertsEl.textContent = alertsActive ? 'Active' : 'Not Configured';
                alertsEl.className = `badge bg-${alertsActive ? 'success' : 'secondary'}`;
            }
            
            // Uptime from health response
            if (health.uptime) {
                const uptimeEl = document.getElementById('health-uptime');
                if (uptimeEl) uptimeEl.textContent = health.uptime;
            }
        }
        
        if (metrics) {
            // Uptime
            document.getElementById('health-uptime').textContent = this.formatUptime(metrics.uptime_seconds);
            
            // Memory
            const memAlloc = metrics.memory_alloc_mb || 0;
            const memSys = metrics.memory_sys_mb || 0;
            document.getElementById('health-memory').textContent = memAlloc.toFixed(1) + ' MB';
            document.getElementById('health-mem-alloc').textContent = memAlloc.toFixed(1) + ' MB';
            document.getElementById('health-mem-sys').textContent = memSys.toFixed(1) + ' MB';
            
            // Progress bars (assuming 512MB max for visualization)
            const maxMem = 512;
            document.getElementById('health-mem-bar').style.width = Math.min((memAlloc / maxMem) * 100, 100) + '%';
            document.getElementById('health-sys-bar').style.width = Math.min((memSys / maxMem) * 100, 100) + '%';
            
            // Goroutines
            const goroutines = metrics.goroutines || 0;
            document.getElementById('health-goroutines').textContent = goroutines;
            document.getElementById('health-go-count').textContent = goroutines;
            document.getElementById('health-go-bar').style.width = Math.min((goroutines / 100) * 100, 100) + '%';
            
            // Last update
            document.getElementById('health-last-update').textContent = new Date().toLocaleTimeString();
        }
        
        // Add system events
        this.loadHealthEvents();
    },
    
    async loadHealthEvents() {
        const tbody = document.getElementById('health-events');
        if (!tbody) return;
        
        // Try to get real events from audit API
        try {
            const auditLogs = await this.api('/api/admin/audit?limit=10');
            
            if (auditLogs && Array.isArray(auditLogs) && auditLogs.length > 0) {
                // Real audit events
                tbody.innerHTML = auditLogs.map(log => {
                    const time = log.time || log.timestamp || log.created_at;
                    const formattedTime = time ? new Date(time).toLocaleTimeString() : '-';
                    const eventType = log.type || log.action || 'Event';
                    const details = log.message || log.request_uri || log.details || '-';
                    const severity = log.severity || 'info';
                    const statusClass = severity === 'HIGH' ? 'danger' : severity === 'MEDIUM' ? 'warning' : 'success';
                    
                    return `
                        <tr>
                            <td class="text-muted">${escapeHtml(formattedTime)}</td>
                            <td>${escapeHtml(eventType)}</td>
                            <td class="small">${escapeHtml(details)}</td>
                            <td><span class="badge bg-${escapeAttr(statusClass)}">${escapeHtml(severity.toLowerCase())}</span></td>
                        </tr>
                    `;
                }).join('');
                return;
            }
        } catch (e) {
            console.log('Could not fetch audit logs, using system events');
        }
        
        // Fallback: Generate events based on current health status
        const health = await this.api('/api/health');
        const events = [];
        const now = Date.now();
        
        // Add startup event
        events.push({
            time: new Date(now - 1000).toLocaleTimeString(),
            event: 'Health Check',
            details: `System status: ${health?.status || 'unknown'}`,
            status: health?.status === 'healthy' ? 'success' : 'warning'
        });
        
        // Database event
        const dbStatus = health?.databases?.postgres || 'not_configured';
        events.push({
            time: new Date(now - 5000).toLocaleTimeString(),
            event: 'Database Status',
            details: `PostgreSQL: ${dbStatus}`,
            status: dbStatus === 'connected' ? 'success' : 'warning'
        });
        
        // WAF event
        if (health?.features?.waf) {
            events.push({
                time: new Date(now - 10000).toLocaleTimeString(),
                event: 'WAF Engine',
                details: 'Protection active',
                status: 'success'
            });
        }
        
        // Version info
        events.push({
            time: new Date(now - 30000).toLocaleTimeString(),
            event: 'System Start',
            details: `Version ${health?.version || 'unknown'}`,
            status: 'info'
        });
        
        tbody.innerHTML = events.map(e => `
            <tr>
                <td class="text-muted">${escapeHtml(e.time)}</td>
                <td>${escapeHtml(e.event)}</td>
                <td class="small">${escapeHtml(e.details)}</td>
                <td><span class="badge bg-${escapeAttr(e.status)}">${escapeHtml(e.status)}</span></td>
            </tr>
        `).join('');
    },
    
    refreshHealth() {
        this.showToast('Info', 'Refreshing health data...');
        this.fetchHealthData();
    },

    // ============================================
    // SECURITY SETTINGS FUNCTIONS
    // ============================================
    
    async fetchSecurityData() {
        // Fetch all security data
        await Promise.all([
            this.fetchAPIKeys(),
            this.fetchIPAllowlist(),
            this.fetchSecurityOverview()
        ]);
    },
    
    async fetchSecurityOverview() {
        const data = await this.api('/api/security/overview');
        if (data) {
            document.getElementById('security-api-keys').textContent = data.api_keys_count || '0';
            document.getElementById('security-ip-count').textContent = data.ip_allowlist_count || '0';
        }
    },
    
    async fetchAPIKeys() {
        const data = await this.api('/api/security/apikeys');
        const tbody = document.getElementById('apikeys-tbody');
        if (!tbody) return;
        
        if (!data || !data.keys || data.keys.length === 0) {
            tbody.innerHTML = '<tr><td colspan="6" class="text-center text-muted">No API keys configured</td></tr>';
            document.getElementById('security-api-keys').textContent = '0';
            return;
        }
        
        document.getElementById('security-api-keys').textContent = data.keys.length;
        
        tbody.innerHTML = data.keys.map(key => `
            <tr>
                <td>${escapeHtml(key.name)}</td>
                <td><code>${escapeHtml(key.key_prefix)}</code></td>
                <td>${key.scopes ? escapeHtml(key.scopes.join(', ')) : '-'}</td>
                <td>${new Date(key.created_at).toLocaleDateString()}</td>
                <td>${new Date(key.expires_at).toLocaleDateString()}</td>
                <td>
                    <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.revokeAPIKey('${escapeAttr(key.id)}')">
                        <i class="fas fa-trash"></i>
                    </button>
                </td>
            </tr>
        `).join('');
    },
    
    async createAPIKey() {
        const name = document.getElementById('apikey-name')?.value;
        const rawScopes = document.getElementById('apikey-scopes')?.value || '';
        const scopes = rawScopes
            .split(',')
            .map(s => s.trim())
            .filter(Boolean);
        const expiresIn = parseInt(document.getElementById('apikey-expires')?.value || '30');
        
        if (!name) {
            this.showToast('Error', 'Please enter a key name', true);
            return;
        }
        
        const res = await this.api('/api/security/apikeys/create', {
            method: 'POST',
            body: JSON.stringify({
                name,
                scopes,
                expires_in: `${expiresIn * 24}h`
            })
        });
        
        if (res?.success) {
            const createdKey = res.key || res.full_key || '';
            this.showToast('Success', `API Key created! Key: ${createdKey}`);
            // Show the key in an alert so user can copy it
            alert(`API Key Created!\n\nKey: ${createdKey}\n\nSave this key now - it won't be shown again!`);
            await this.fetchAPIKeys();
            bootstrap.Modal.getInstance(document.getElementById('apiKeyModal'))?.hide();
        } else {
            this.showToast('Error', 'Failed to create API key', true);
        }
    },
    
    async revokeAPIKey(keyId) {
        if (!confirm('Are you sure you want to revoke this API key?')) return;
        
        const res = await this.api('/api/security/apikeys/revoke', {
            method: 'POST',
            body: JSON.stringify({ key_id: keyId })
        });
        
        if (res?.success) {
            this.showToast('Success', 'API key revoked');
            await this.fetchAPIKeys();
        } else {
            this.showToast('Error', 'Failed to revoke API key', true);
        }
    },
    
    async fetchIPAllowlist() {
        const data = await this.api('/api/security/ipallowlist');
        const tbody = document.getElementById('ipallow-tbody');
        const enabledCheckbox = document.getElementById('ipAllowlistEnabled');
        
        if (!tbody) return;
        
        if (enabledCheckbox) {
            enabledCheckbox.checked = data?.enabled || false;
        }
        
        if (!data || !data.entries || data.entries.length === 0) {
            tbody.innerHTML = '<tr><td colspan="4" class="text-center text-muted">No IPs whitelisted</td></tr>';
            document.getElementById('security-ip-count').textContent = '0';
            return;
        }
        
        document.getElementById('security-ip-count').textContent = data.entries.length;
        
        tbody.innerHTML = data.entries.map(entry => `
            <tr>
                <td><code>${escapeHtml(entry.ip)}</code></td>
                <td>${escapeHtml(entry.description || '-')}</td>
                <td>${new Date(entry.added_at).toLocaleDateString()}</td>
                <td>
                    <button class="btn btn-sm btn-outline-danger" onclick="ObsidianApp.removeIPFromAllowlist('${escapeAttr(entry.ip)}')">
                        <i class="fas fa-trash"></i>
                    </button>
                </td>
            </tr>
        `).join('');
    },
    
    async addIPToAllowlist() {
        const ip = document.getElementById('ip-address')?.value?.trim();
        const description = document.getElementById('ip-description')?.value?.trim();
        
        if (!ip) {
            this.showToast('Error', 'Please enter an IP address', true);
            return;
        }
        
        // Validate IP format with proper octet and CIDR range checking
        const validateIP = (ipStr) => {
            // Check for CIDR notation
            const parts = ipStr.split('/');
            const ipPart = parts[0];
            const cidrPart = parts[1];
            
            // Validate octets (must be 0-255)
            const octets = ipPart.split('.');
            if (octets.length !== 4) return false;
            
            for (const octet of octets) {
                const num = parseInt(octet, 10);
                if (isNaN(num) || num < 0 || num > 255 || octet !== num.toString()) {
                    return false;
                }
            }
            
            // Validate CIDR if present (must be 0-32)
            if (cidrPart !== undefined) {
                const cidr = parseInt(cidrPart, 10);
                if (isNaN(cidr) || cidr < 0 || cidr > 32 || cidrPart !== cidr.toString()) {
                    return false;
                }
            }
            
            return true;
        };
        
        if (!validateIP(ip)) {
            this.showToast('Error', 'Invalid IP address. Use format: 192.168.1.1 or 192.168.1.0/24 (octets 0-255, CIDR 0-32)', true);
            return;
        }
        
        try {
            const res = await this.api('/api/security/ipallowlist/add', {
                method: 'POST',
                body: JSON.stringify({ ip, description: description || 'Added via dashboard' })
            });
            
            if (res && res.success) {
                this.showToast('Success', `IP ${ip} added to allowlist`);
                // Close modal first
                const modal = bootstrap.Modal.getInstance(document.getElementById('ipModal'));
                if (modal) modal.hide();
                // Then refresh data
                setTimeout(() => this.fetchIPAllowlist(), 300);
            } else {
                this.showToast('Error', res?.error || 'Failed to add IP', true);
            }
        } catch (err) {
            console.error('Add IP error:', err);
            this.showToast('Error', 'Network error. Please try again.', true);
        }
    },
    
    async removeIPFromAllowlist(ip) {
        if (!confirm(`Remove ${ip} from allowlist?`)) return;
        
        const res = await this.api('/api/security/ipallowlist/remove', {
            method: 'POST',
            body: JSON.stringify({ ip })
        });
        
        if (res?.success) {
            this.showToast('Success', 'IP removed from allowlist');
            await this.fetchIPAllowlist();
        } else {
            this.showToast('Error', 'Failed to remove IP', true);
        }
    },
    
    async checkPasswordBreach(password) {
        const pwd = password || document.getElementById('hibp-password')?.value;
        if (!pwd) {
            this.showToast('Warning', 'Please enter a password to check', true);
            return;
        }
        
        const resultDiv = document.getElementById('hibp-result');
        resultDiv.innerHTML = '<div class="spinner-border spinner-border-sm text-warning"></div> Checking...';
        
        try {
            // Use backend API to avoid CORS issues with direct HIBP calls
            const res = await this.api('/api/security/password/check', {
                method: 'POST',
                body: JSON.stringify({ password: pwd })
            });
            
            if (!res) {
                resultDiv.innerHTML = `<div class="alert alert-warning">Failed to check password. Please try again.</div>`;
                return;
            }
            
            if (res.error) {
                resultDiv.innerHTML = `<div class="alert alert-warning">${escapeHtml(res.error)}</div>`;
                return;
            }
            
            if (res.compromised) {
                resultDiv.innerHTML = `
                    <div class="alert alert-danger">
                        <i class="fas fa-exclamation-triangle me-2"></i>
                        <strong>Password Compromised!</strong><br>
                        This password has been seen <strong>${res.count.toLocaleString()}</strong> times in data breaches. Do not use it!
                    </div>`;
            } else {
                resultDiv.innerHTML = `
                    <div class="alert alert-success">
                        <i class="fas fa-check-circle me-2"></i>
                        <strong>Password Safe</strong><br>
                        This password was not found in any known data breaches.
                    </div>`;
            }
        } catch (e) {
            console.error('Password check error:', e);
            resultDiv.innerHTML = `<div class="alert alert-warning">Failed to check password: ${escapeHtml(e.message)}</div>`;
        }
    },
    
    // Toggle password visibility
    togglePasswordVisibility(inputId) {
        const input = document.getElementById(inputId);
        const icon = document.getElementById(inputId + '-eye');
        if (!input) return;
        
        if (input.type === 'password') {
            input.type = 'text';
            if (icon) icon.className = 'fas fa-eye-slash';
        } else {
            input.type = 'password';
            if (icon) icon.className = 'fas fa-eye';
        }
    },
    
    async rotateSecret(secretType) {
        if (!confirm(`Are you sure you want to rotate the ${secretType.toUpperCase()} secret? This will invalidate all existing tokens.`)) {
            return;
        }
        
        const res = await this.api('/api/security/secrets/rotate-jwt', { method: 'POST' });
        if (res?.success) {
            this.showToast('Success', 'Secret rotated successfully. All users will need to re-authenticate.');
        } else {
            this.showToast('Error', 'Failed to rotate secret', true);
        }
    },
    
    async reloadSecrets() {
        const res = await this.api('/api/security/secrets/reload', { method: 'POST' });
        if (res?.success) {
            this.showToast('Success', 'Secrets reloaded from environment');
        } else {
            this.showToast('Error', 'Failed to reload secrets', true);
        }
    },
    
    showCreateApiKeyModal() {
        document.getElementById('apiKeyForm')?.reset();
        new bootstrap.Modal(document.getElementById('apiKeyModal')).show();
    },
    
    showAddIpModal() {
        document.getElementById('ipForm')?.reset();
        new bootstrap.Modal(document.getElementById('ipModal')).show();
    },

    // Logout
    logout() {
        localStorage.removeItem('obsidian_token');
        localStorage.removeItem('obsidian_user');
        globalThis.location.href = '/login.html';
    },

    // ============================================
    // GRAPHQL ANALYSIS
    // ============================================
    async analyzeGraphQL() {
        const query = document.getElementById('graphql-query')?.value?.trim();
        const resultDiv = document.getElementById('graphql-result');
        if (!query) {
            this.showToast('Error', 'Please enter a GraphQL query', true);
            return;
        }
        resultDiv.innerHTML = '<div class="spinner-border spinner-border-sm text-warning"></div> Analyzing...';
        try {
            const res = await this.api('/api/security/graphql/analyze', {
                method: 'POST',
                body: JSON.stringify({ query })
            });
            if (!res) { resultDiv.innerHTML = '<div class="alert alert-warning">Analysis failed</div>'; return; }
            const depthClass = res.depth > 8 ? 'danger' : res.depth > 5 ? 'warning' : 'success';
            const complexClass = res.complexity > 80 ? 'danger' : res.complexity > 40 ? 'warning' : 'success';
            resultDiv.innerHTML = `
                <div class="row g-3 mt-2">
                    <div class="col-md-3"><div class="stat-card text-center"><div class="stat-title">Depth</div><div class="h3 text-${depthClass}">${escapeHtml(String(res.depth || 0))}</div></div></div>
                    <div class="col-md-3"><div class="stat-card text-center"><div class="stat-title">Complexity</div><div class="h3 text-${complexClass}">${escapeHtml(String(res.complexity || 0))}</div></div></div>
                    <div class="col-md-3"><div class="stat-card text-center"><div class="stat-title">Aliases</div><div class="h3 text-info">${escapeHtml(String(res.aliases || 0))}</div></div></div>
                    <div class="col-md-3"><div class="stat-card text-center"><div class="stat-title">Fields</div><div class="h3 text-muted">${escapeHtml(String(res.fields || 0))}</div></div></div>
                </div>
                ${res.blocked ? '<div class="alert alert-danger mt-3"><i class="fas fa-ban me-2"></i>This query would be <strong>BLOCKED</strong> by current limits.</div>' : '<div class="alert alert-success mt-3"><i class="fas fa-check me-2"></i>This query passes all security checks.</div>'}
            `;
        } catch (e) {
            resultDiv.innerHTML = `<div class="alert alert-danger">${escapeHtml(e.message)}</div>`;
        }
    },

    async updateGraphQLConfig() {
        const config = {
            max_depth: parseInt(document.getElementById('gql-max-depth')?.value || '10'),
            max_complexity: parseInt(document.getElementById('gql-max-complexity')?.value || '100'),
            max_aliases: parseInt(document.getElementById('gql-max-aliases')?.value || '10')
        };
        const res = await this.api('/api/security/graphql/config', {
            method: 'POST',
            body: JSON.stringify(config)
        });
        if (res?.success) {
            this.showToast('Success', 'GraphQL limits updated');
        } else {
            this.showToast('Error', res?.error || 'Failed to update config', true);
        }
    },

    // ============================================
    // RESPONSE BODY INSPECTION
    // ============================================
    async updateRespBodyConfig() {
        const config = {
            enabled: document.getElementById('respbody-enabled')?.checked ?? true,
            max_size: parseInt(document.getElementById('respbody-maxsize')?.value || '1048576'),
            custom_regex: document.getElementById('respbody-regex')?.value || ''
        };
        const res = await this.api('/api/security/respbody/config', {
            method: 'POST',
            body: JSON.stringify(config)
        });
        if (res?.success) {
            this.showToast('Success', 'Response body config updated');
        } else {
            this.showToast('Error', res?.error || 'Failed to update config', true);
        }
    },

    async testRespBody() {
        const body = document.getElementById('respbody-test-input')?.value?.trim();
        const resultDiv = document.getElementById('respbody-result');
        if (!body) {
            this.showToast('Error', 'Please enter response body content to test', true);
            return;
        }
        resultDiv.innerHTML = '<div class="spinner-border spinner-border-sm text-warning"></div> Scanning...';
        try {
            const res = await this.api('/api/security/respbody/test', {
                method: 'POST',
                body: JSON.stringify({ body })
            });
            if (!res) { resultDiv.innerHTML = '<div class="alert alert-warning">Test failed</div>'; return; }
            if (res.findings && res.findings.length > 0) {
                resultDiv.innerHTML = `
                    <div class="alert alert-danger"><i class="fas fa-exclamation-triangle me-2"></i><strong>${res.findings.length} sensitive data pattern(s) detected!</strong></div>
                    <ul class="list-group">${res.findings.map(f => `<li class="list-group-item bg-dark border-secondary text-light"><i class="fas fa-bug text-danger me-2"></i>${escapeHtml(f.type || f)}: ${escapeHtml(f.match || '')}</li>`).join('')}</ul>
                `;
            } else {
                resultDiv.innerHTML = '<div class="alert alert-success"><i class="fas fa-check me-2"></i>No sensitive data patterns found.</div>';
            }
        } catch (e) {
            resultDiv.innerHTML = `<div class="alert alert-danger">${escapeHtml(e.message)}</div>`;
        }
    },

    // ============================================
    // CACHE & RATE LIMIT ADMIN
    // ============================================
    async fetchCacheStats() {
        try {
            const stats = await this.api('/api/cache/stats');
            if (stats) {
                const hitRate = (stats.hits && stats.misses) 
                    ? ((stats.hits / (stats.hits + stats.misses)) * 100).toFixed(1) + '%' 
                    : '-';
                document.getElementById('cache-hit-rate').textContent = hitRate;
                document.getElementById('cache-total-keys').textContent = stats.total_keys || '-';
                document.getElementById('cache-memory').textContent = stats.memory_used || '-';
            }
        } catch (e) {
            console.log('Cache stats not available');
        }
    },

    async resetRateLimits() {
        if (!confirm('Reset all rate limit counters? This will allow previously rate-limited IPs to access the system again.')) return;
        const resultDiv = document.getElementById('ratelimit-reset-result');
        const res = await this.api('/api/admin/ratelimit/reset', { method: 'POST' });
        if (res?.success) {
            this.showToast('Success', 'All rate limits have been reset');
            if (resultDiv) resultDiv.innerHTML = '<div class="alert alert-success mt-2"><i class="fas fa-check me-2"></i>Rate limits cleared.</div>';
        } else {
            this.showToast('Error', res?.error || 'Failed to reset rate limits', true);
            if (resultDiv) resultDiv.innerHTML = '<div class="alert alert-danger mt-2">Reset failed.</div>';
        }
    }
};

// Initialize on load
document.addEventListener('DOMContentLoaded', () => ObsidianApp.init());

// Global functions for inline handlers
function loadView(view) { ObsidianApp.loadView(view); }
function logout() { ObsidianApp.logout(); }
function openRuleModal(id) { ObsidianApp.openRuleModal(id); }
function saveRule() { ObsidianApp.saveRule(); }
function deleteRule(id) { ObsidianApp.deleteRule(id); }

// Security Settings global functions
function checkPasswordBreach() { ObsidianApp.checkPasswordBreach(); }
function rotateSecret(type) { ObsidianApp.rotateSecret(type); }
function reloadSecrets() { ObsidianApp.reloadSecrets(); }
function showCreateApiKeyModal() { 
    document.getElementById('apiKeyForm')?.reset();
    new bootstrap.Modal(document.getElementById('apiKeyModal')).show(); 
}
function showAddIpModal() { 
    document.getElementById('ipForm')?.reset();
    new bootstrap.Modal(document.getElementById('ipModal')).show(); 
}
