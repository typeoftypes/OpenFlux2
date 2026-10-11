import Foundation
import CryptoKit
import Security

/// Encrypts profile JSON on disk under Application Support.
/// Symmetric key lives in the Keychain (device-local, not iCloud by default).
enum ProfileVault {
    private static let fileName = "profiles.vault"
    private static let keyTag = "com.typeoftypes.openflux.profile-vault-key"
    private static let legacyUDKey = "openflux.profiles.v1"
    private static let legacySelKey = "openflux.selectedProfile"

    struct Payload: Codable {
        var profiles: [Profile]
        var selectedID: UUID?
    }

    private static var fileURL: URL {
        let dir = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let app = dir.appendingPathComponent("OpenFlux", isDirectory: true)
        try? FileManager.default.createDirectory(at: app, withIntermediateDirectories: true)
        return app.appendingPathComponent(fileName)
    }

    static func load() -> Payload? {
        // Prefer encrypted vault on disk
        if let data = try? Data(contentsOf: fileURL),
           let key = symmetricKey(),
           let plain = try? decrypt(data, key: key),
           let payload = try? JSONDecoder().decode(Payload.self, from: plain) {
            return payload
        }
        // Migrate once from UserDefaults (plain)
        if let data = UserDefaults.standard.data(forKey: legacyUDKey),
           let list = try? JSONDecoder().decode([Profile].self, from: data) {
            var sel: UUID?
            if let s = UserDefaults.standard.string(forKey: legacySelKey) {
                sel = UUID(uuidString: s)
            }
            let payload = Payload(profiles: list, selectedID: sel)
            _ = save(payload)
            UserDefaults.standard.removeObject(forKey: legacyUDKey)
            return payload
        }
        return nil
    }

    @discardableResult
    static func save(_ payload: Payload) -> Bool {
        guard let key = symmetricKey(),
              let plain = try? JSONEncoder().encode(payload),
              let blob = try? encrypt(plain, key: key) else { return false }
        do {
            try blob.write(to: fileURL, options: [.atomic, .completeFileProtection])
            return true
        } catch {
            return false
        }
    }

    // MARK: - Crypto

    private static func symmetricKey() -> SymmetricKey? {
        if let existing = loadKey() { return existing }
        let key = SymmetricKey(size: .bits256)
        let bytes = key.withUnsafeBytes { Data($0) }
        guard storeKey(bytes) else { return key } // still use ephemeral if Keychain fails
        return key
    }

    private static func encrypt(_ plain: Data, key: SymmetricKey) throws -> Data {
        let box = try AES.GCM.seal(plain, using: key)
        guard let combined = box.combined else {
            throw NSError(domain: "ProfileVault", code: 1)
        }
        return combined
    }

    private static func decrypt(_ blob: Data, key: SymmetricKey) throws -> Data {
        let box = try AES.GCM.SealedBox(combined: blob)
        return try AES.GCM.open(box, using: key)
    }

    private static func loadKey() -> SymmetricKey? {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrAccount as String: keyTag,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: AnyObject?
        let st = SecItemCopyMatching(q as CFDictionary, &out)
        guard st == errSecSuccess, let data = out as? Data else { return nil }
        return SymmetricKey(data: data)
    }

    private static func storeKey(_ data: Data) -> Bool {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrAccount as String: keyTag,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        SecItemDelete(q as CFDictionary)
        return SecItemAdd(q as CFDictionary, nil) == errSecSuccess
    }
}
