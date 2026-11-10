import Foundation

/// The iPhones a computer reaches that the assistant may operate through it (the hub's extension iphone,
/// docs/extensions/iphone.md): `Machine.iphone`, nil while the extension is off or the computer can't reach iPhones.
nonisolated struct MachineIPhone: Codable, Hashable, Sendable {
    /// The computer's switch: Allow the Assistant to Operate iPhones Through This Computer.
    var enabled = false
    /// The device bridge is on the computer; `version`, its version.
    var installed = false
    var version: String? = nil
    var devices: [IPhoneDevice] = []
    /// What is missing on the computer as a whole (no bridge, nothing paired, an old node), in the request's language.
    var note: String? = nil
    /// Pairing an iPhone from the app (kind pair | forget): the running one, or the last.
    var job: ComputerJob? = nil

    enum CodingKeys: String, CodingKey { case enabled, installed, version, devices, note, job }

    init(enabled: Bool = false, installed: Bool = false, version: String? = nil, devices: [IPhoneDevice] = [], note: String? = nil,
         job: ComputerJob? = nil) {
        self.enabled = enabled
        self.installed = installed
        self.version = version
        self.devices = devices
        self.note = note
        self.job = job
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        installed = try c.decodeIfPresent(Bool.self, forKey: .installed) ?? false
        version = try c.decodeIfPresent(String.self, forKey: .version)
        devices = try c.decodeIfPresent([IPhoneDevice].self, forKey: .devices) ?? []
        note = try c.decodeIfPresent(String.self, forKey: .note)
        job = try? c.decodeIfPresent(ComputerJob.self, forKey: .job)
    }
}

/// One iPhone as the computer's node sees it. `id` is the node's stable id for it; `productType` ("iPhone17,1")
/// and `ios` ("26.1") are how the app on that iPhone tells it is this one.
nonisolated struct IPhoneDevice: Codable, Hashable, Sendable, Identifiable {
    var id: String
    var name: String
    var productType: String? = nil
    var ios: String? = nil
    /// The computer holds a pairing record for it; `usb`: it is plugged in now (one that is can be paired).
    var paired = false
    var usb = false
    /// network | usb, while connected.
    var transport: String? = nil
    var connected = false
    var locked = false
    var ready = false
    var note: String? = nil
    /// The computer is on and this iPhone consented: the assistant may operate it.
    var allowed = false

    enum CodingKeys: String, CodingKey {
        case id, name, ios, paired, usb, transport, connected, locked, ready, note, allowed
        case productType = "product_type"
    }

    init(id: String, name: String, productType: String? = nil, ios: String? = nil, paired: Bool = true, usb: Bool = false,
         transport: String? = nil, connected: Bool = false, locked: Bool = false, ready: Bool = false, note: String? = nil,
         allowed: Bool = false) {
        self.id = id
        self.name = name
        self.productType = productType
        self.ios = ios
        self.paired = paired
        self.usb = usb
        self.transport = transport
        self.connected = connected
        self.locked = locked
        self.ready = ready
        self.note = note
        self.allowed = allowed
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decodeIfPresent(String.self, forKey: .name) ?? ""
        productType = try c.decodeIfPresent(String.self, forKey: .productType)
        ios = try c.decodeIfPresent(String.self, forKey: .ios)
        paired = try c.decodeIfPresent(Bool.self, forKey: .paired) ?? false
        usb = try c.decodeIfPresent(Bool.self, forKey: .usb) ?? false
        transport = try c.decodeIfPresent(String.self, forKey: .transport)
        connected = try c.decodeIfPresent(Bool.self, forKey: .connected) ?? false
        locked = try c.decodeIfPresent(Bool.self, forKey: .locked) ?? false
        ready = try c.decodeIfPresent(Bool.self, forKey: .ready) ?? false
        note = try c.decodeIfPresent(String.self, forKey: .note)
        allowed = try c.decodeIfPresent(Bool.self, forKey: .allowed) ?? false
    }
}

/// One iPhone in `GET /api/v1/iphone`: the device (each once, whichever computers reach it), the computers, and
/// whether it consented.
nonisolated struct IPhoneSeen: Codable, Hashable, Sendable, Identifiable {
    struct Mac: Codable, Hashable, Sendable {
        var id: String
        var name: String
        var enabled = false

        enum CodingKeys: String, CodingKey { case id, name, enabled }

        init(id: String, name: String, enabled: Bool = false) {
            self.id = id
            self.name = name
            self.enabled = enabled
        }

        init(from decoder: Decoder) throws {
            let c = try decoder.container(keyedBy: CodingKeys.self)
            id = try c.decode(String.self, forKey: .id)
            name = try c.decodeIfPresent(String.self, forKey: .name) ?? id
            enabled = try c.decodeIfPresent(Bool.self, forKey: .enabled) ?? false
        }
    }

    var device: IPhoneDevice
    var machines: [Mac] = []
    var consented = false

    var id: String { device.id }

    enum CodingKeys: String, CodingKey { case machines, consented }

    init(device: IPhoneDevice, machines: [Mac] = [], consented: Bool = false) {
        self.device = device
        self.machines = machines
        self.consented = consented
    }

    init(from decoder: Decoder) throws {
        device = try IPhoneDevice(from: decoder) // the device's fields are at the same level
        let c = try decoder.container(keyedBy: CodingKeys.self)
        machines = try c.decodeIfPresent([Mac].self, forKey: .machines) ?? []
        consented = try c.decodeIfPresent(Bool.self, forKey: .consented) ?? false
    }

    func encode(to encoder: Encoder) throws {
        try device.encode(to: encoder)
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(machines, forKey: .machines)
        try c.encode(consented, forKey: .consented)
    }

    /// The computers that reach it, by name.
    var macNames: String { machines.map(\.name).joined(separator: ", ") }
    /// Some computer that reaches it is turned on.
    var macOn: Bool { machines.contains { $0.enabled } }
}

/// `GET /api/v1/iphone`: whether the extension is on, and every iPhone the computers reach.
nonisolated struct IPhoneList: Codable, Hashable, Sendable {
    var on = false
    var devices: [IPhoneSeen] = []
}

/// Which of the iPhones the computers list is the one this app runs on. The app cannot read its own device id (iOS gives
/// apps no UDID), so it matches its model identifier and iOS version; several alike, the user picks one. Anything
/// else fails closed: no match, no switch.
nonisolated enum IPhoneIdentity {
    enum Match: Equatable {
        /// This is the one; `alike` is how many matched (more than one: it was picked, and can be picked again).
        case this(IPhoneSeen, alike: Int)
        /// Several look like this iPhone: the user picks.
        case choose([IPhoneSeen])
        /// None of them is this iPhone (not paired with an online computer, or the model and version do not match).
        case none
    }

    /// devices matched against model ("iPhone17,1") and ios ("26.1.1"); picked is the id picked before, if any.
    static func match(_ devices: [IPhoneSeen], model: String, ios: String, picked: String?) -> Match {
        let alike = devices.filter { d in
            guard let type = d.device.productType, let version = d.device.ios, !model.isEmpty else { return false }
            return type == model && same(version, ios)
        }
        if let picked, let d = alike.first(where: { $0.id == picked }) { return .this(d, alike: alike.count) }
        switch alike.count {
        case 0: return .none
        case 1: return .this(alike[0], alike: 1)
        default: return .choose(alike)
        }
    }

    /// The same iOS version, written either way ("26.1" and "26.1.0").
    static func same(_ a: String, _ b: String) -> Bool {
        func parts(_ v: String) -> [Int] {
            var p = v.split(separator: ".").map { Int($0) ?? -1 }
            while p.count > 1, p.last == 0 { p.removeLast() }
            return p
        }
        return !a.isEmpty && parts(a) == parts(b)
    }
}
