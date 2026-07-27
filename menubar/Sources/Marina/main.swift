// Marina's menu bar agent.
//
// It owns no state of its own: it polls the daemon's /api/state and renders
// whatever it finds. If the daemon is down the icon says so rather than
// pretending, and the menu offers the one action that fixes it.

import AppKit
import Foundation

// MARK: - Wire format

struct Snapshot: Decodable {
    let counts: Counts
    let services: [Service]
    let store: StoreHealth
    let version: String
}

struct Counts: Decodable {
    let total, apps, infra, system, http: Int
    /// Apps you would actually open, and the supporting services behind them.
    /// The icon shows `primary`, because `apps` counts every worker port too and
    /// badly overstates how much is running.
    let primary, services: Int
}

struct StoreHealth: Decodable {
    let connected: Bool
    let error: String?
}

struct Meta: Decodable {
    let pinned: Bool
}

struct Service: Decodable {
    let key: String
    let port: Int
    let kind: String
    let display: String
    let project: String?
    let subpath: String?
    let entry: String?
    let framework: String?
    let url: String?
    let startedAt: Int?
    let role: String
    let serviceCount: Int?
    let primaryPort: Int?
    let meta: Meta

    /// What to call this berth in a menu that already groups by project.
    var shortLabel: String {
        if let entry, !entry.isEmpty { return entry }
        if let subpath, !subpath.isEmpty { return subpath }
        return display
    }

    var groupName: String {
        if let project, !project.isEmpty { return project }
        return display
    }

    var uptime: String? {
        guard let startedAt, startedAt > 0 else { return nil }
        let secs = max(0, Int(Date().timeIntervalSince1970) - startedAt)
        if secs < 60 { return "\(secs)s" }
        if secs < 3600 { return "\(secs / 60)m" }
        if secs < 86400 { return "\(secs / 3600)h \((secs % 3600) / 60)m" }
        return "\(secs / 86400)d \((secs % 86400) / 3600)h"
    }
}

// MARK: - Daemon client

/// Reads the daemon's state. Kept deliberately dumb: one request, short timeout,
/// no retry logic beyond the next poll tick.
final class DaemonClient {
    let address: String
    private let session: URLSession

    init(address: String) {
        self.address = address
        let config = URLSessionConfiguration.ephemeral
        config.timeoutIntervalForRequest = 3
        config.requestCachePolicy = .reloadIgnoringLocalCacheData
        self.session = URLSession(configuration: config)
    }

    var dashboardURL: URL? { URL(string: "http://\(address)/") }

    func fetch(completion: @escaping (Snapshot?) -> Void) {
        guard let url = URL(string: "http://\(address)/api/state") else {
            completion(nil)
            return
        }
        session.dataTask(with: url) { data, _, _ in
            let snapshot = data.flatMap { try? JSONDecoder().decode(Snapshot.self, from: $0) }
            DispatchQueue.main.async { completion(snapshot) }
        }.resume()
    }
}

// MARK: - Menu bar controller

@MainActor
final class MarinaMenuController: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.variableLength)
    private let client: DaemonClient
    private var timer: Timer?
    private var snapshot: Snapshot?
    private var reachable = false

    init(address: String) {
        self.client = DaemonClient(address: address)
        super.init()
    }

    func applicationDidFinishLaunching(_ notification: Notification) {
        statusItem.menu = NSMenu()
        statusItem.menu?.delegate = self
        render()
        refresh()

        // 3s keeps the count honest without being a noticeable wakeup source.
        timer = Timer.scheduledTimer(withTimeInterval: 3, repeats: true) { [weak self] _ in
            Task { @MainActor in self?.refresh() }
        }
        timer?.tolerance = 1
    }

    private func refresh() {
        client.fetch { [weak self] snapshot in
            guard let self else { return }
            self.snapshot = snapshot
            self.reachable = snapshot != nil
            self.render()
        }
    }

    // MARK: Icon

    private func render() {
        guard let button = statusItem.button else { return }

        let symbol = reachable ? "water.waves" : "water.waves.slash"
        let config = NSImage.SymbolConfiguration(pointSize: 13, weight: .medium)
        let image = NSImage(systemSymbolName: symbol, accessibilityDescription: "Marina")?
            .withSymbolConfiguration(config)
        image?.isTemplate = true
        button.image = image
        button.imagePosition = .imageLeading

        if let counts = snapshot?.counts {
            button.attributedTitle = NSAttributedString(
                string: " \(counts.primary)",
                attributes: [
                    .font: NSFont.monospacedDigitSystemFont(ofSize: 12, weight: .medium)
                ]
            )
            button.toolTip = "Marina — \(counts.primary) apps, \(counts.services) services, "
                + "\(counts.infra) infra, \(counts.http) answering HTTP"
        } else {
            button.attributedTitle = NSAttributedString(string: "")
            button.toolTip = "Marina — daemon not responding"
        }

        rebuildMenu()
    }

    // MARK: Menu

    private func rebuildMenu() {
        guard let menu = statusItem.menu else { return }
        menu.removeAllItems()

        guard let snapshot, reachable else {
            addDisabled("Daemon not responding", to: menu)
            menu.addItem(.separator())
            add("Start the daemon", to: menu, action: #selector(restartDaemon))
            addQuit(to: menu)
            return
        }

        addDisabled(
            "\(snapshot.counts.primary) apps · \(snapshot.counts.services) services · "
                + "\(snapshot.counts.total) ports",
            to: menu
        )
        menu.addItem(.separator())

        let open = add("Open Dashboard", to: menu, action: #selector(openDashboard))
        open.keyEquivalent = "d"
        open.keyEquivalentModifierMask = [.command]
        menu.addItem(.separator())

        let clusters = Self.clusters(from: snapshot.services)

        if clusters.isEmpty {
            addDisabled("No apps listening", to: menu)
        }

        // A cluster is pinned when any part of it is, so pinning a project's
        // front door keeps its services with it instead of scattering them.
        let pinned = clusters.filter(\.isPinned)
        let rest = clusters.filter { !$0.isPinned }

        if !pinned.isEmpty {
            addDisabled("Pinned", to: menu)
            for cluster in pinned { addCluster(cluster, to: menu) }
            menu.addItem(.separator())
        }
        for cluster in rest { addCluster(cluster, to: menu) }

        menu.addItem(.separator())

        let infra = snapshot.services.filter { $0.kind == "infra" }
        if !infra.isEmpty {
            let parent = NSMenuItem(title: "Infrastructure  (\(infra.count))", action: nil, keyEquivalent: "")
            let submenu = NSMenu()
            for service in infra { submenu.addItem(berthItem(service, showProject: true)) }
            parent.submenu = submenu
            menu.addItem(parent)
        }

        addDisabled(
            snapshot.store.connected ? "Postgres connected" : "Postgres offline",
            to: menu
        )

        menu.addItem(.separator())
        add("Restart daemon", to: menu, action: #selector(restartDaemon))
        addQuit(to: menu)
    }

    /// A project front door together with the services that only serve it.
    struct Cluster {
        let primary: Service
        var services: [Service]

        var isPinned: Bool { primary.meta.pinned || services.contains { $0.meta.pinned } }
    }

    /// Folds the flat service list into clusters using the daemon's roles. A
    /// worker whose front door is not listening is promoted rather than dropped,
    /// so no live port can disappear from the menu.
    static func clusters(from services: [Service]) -> [Cluster] {
        let apps = services.filter { $0.kind == "app" }
        var clusters: [Cluster] = []
        var index: [String: Int] = [:]

        for app in apps where app.role != "service" {
            index["\(app.groupName):\(app.port)"] = clusters.count
            clusters.append(Cluster(primary: app, services: []))
        }
        for app in apps where app.role == "service" {
            let key = "\(app.groupName):\(app.primaryPort ?? 0)"
            if let at = index[key] {
                clusters[at].services.append(app)
            } else {
                clusters.append(Cluster(primary: app, services: []))
            }
        }
        for i in clusters.indices {
            clusters[i].services.sort { $0.port < $1.port }
        }
        return clusters
    }

    /// Renders a cluster: the app itself, then its services one level down.
    private func addCluster(_ cluster: Cluster, to menu: NSMenu) {
        menu.addItem(berthItem(cluster.primary, showProject: true))
        guard !cluster.services.isEmpty else { return }

        let count = cluster.services.count
        let noun = count == 1 ? "service" : "services"
        let parent = NSMenuItem(title: "\(count) \(noun)", action: nil, keyEquivalent: "")
        parent.attributedTitle = NSAttributedString(
            string: "      \(count) \(noun)",
            attributes: [
                .font: NSFont.menuFont(ofSize: 11),
                .foregroundColor: NSColor.secondaryLabelColor,
            ]
        )
        let submenu = NSMenu()
        for service in cluster.services { submenu.addItem(berthItem(service, showProject: false)) }
        parent.submenu = submenu
        menu.addItem(parent)
    }

    /// One berth row. Disabled when the port doesn't answer HTTP, because there
    /// is nothing for a click to open.
    private func berthItem(_ service: Service, showProject: Bool) -> NSMenuItem {
        let name = showProject ? service.display : service.shortLabel
        let item = NSMenuItem(title: name, action: nil, keyEquivalent: "")

        let title = NSMutableAttributedString()
        title.append(NSAttributedString(
            string: String(format: "%-6d", service.port),
            attributes: [
                .font: NSFont.monospacedSystemFont(ofSize: 11, weight: .medium),
                .foregroundColor: service.url != nil ? NSColor.secondaryLabelColor : NSColor.tertiaryLabelColor,
            ]
        ))
        title.append(NSAttributedString(
            string: name,
            attributes: [.font: NSFont.menuFont(ofSize: 13)]
        ))
        if let uptime = service.uptime {
            title.append(NSAttributedString(
                string: "   \(uptime)",
                attributes: [
                    .font: NSFont.monospacedSystemFont(ofSize: 10, weight: .regular),
                    .foregroundColor: NSColor.tertiaryLabelColor,
                ]
            ))
        }
        item.attributedTitle = title

        if let url = service.url, let parsed = URL(string: url) {
            item.action = #selector(openService(_:))
            item.target = self
            item.representedObject = parsed
            item.toolTip = "Open \(url)"
        } else {
            item.isEnabled = false
            item.toolTip = "Listening on port \(service.port), but not answering HTTP"
        }
        return item
    }

    @discardableResult
    private func add(_ title: String, to menu: NSMenu, action: Selector) -> NSMenuItem {
        let item = NSMenuItem(title: title, action: action, keyEquivalent: "")
        item.target = self
        menu.addItem(item)
        return item
    }

    private func addDisabled(_ title: String, to menu: NSMenu) {
        let item = NSMenuItem(title: title, action: nil, keyEquivalent: "")
        item.isEnabled = false
        menu.addItem(item)
    }

    private func addQuit(to menu: NSMenu) {
        menu.addItem(.separator())
        let item = add("Quit Marina Menu", to: menu, action: #selector(quit))
        item.keyEquivalent = "q"
        item.keyEquivalentModifierMask = [.command]
    }

    // MARK: Actions

    @objc private func openDashboard() {
        if let url = client.dashboardURL { NSWorkspace.shared.open(url) }
    }

    @objc private func openService(_ sender: NSMenuItem) {
        if let url = sender.representedObject as? URL { NSWorkspace.shared.open(url) }
    }

    @objc private func restartDaemon() {
        let task = Process()
        task.executableURL = URL(fileURLWithPath: "/bin/launchctl")
        task.arguments = ["kickstart", "-k", "gui/\(getuid())/tech.bocchino.marina"]
        try? task.run()
        // Give launchd a moment before asking the daemon how it's doing.
        DispatchQueue.main.asyncAfter(deadline: .now() + 1.5) { [weak self] in
            Task { @MainActor in self?.refresh() }
        }
    }

    @objc private func quit() {
        NSApp.terminate(nil)
    }
}

extension MarinaMenuController: NSMenuDelegate {
    // Refresh on open so the menu is never showing a stale list.
    func menuWillOpen(_ menu: NSMenu) {
        refresh()
    }
}

// MARK: - Entry point

let address = ProcessInfo.processInfo.environment["MARINA_ADDR"] ?? "127.0.0.1:7777"

// Top-level code in main.swift always runs on the main thread, so asserting the
// isolation here is accurate — and it lets the controller stay fully main-actor
// rather than poking holes in it for the sake of construction.
MainActor.assumeIsolated {
    let app = NSApplication.shared
    app.setActivationPolicy(.accessory) // menu bar only, no Dock icon
    let controller = MarinaMenuController(address: address)
    app.delegate = controller
    app.run()
}
