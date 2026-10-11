import Foundation
import CryptoKit
import Security

/// On-device encrypted profile vault.
///
/// Profiles (document URLs, tokens metadata, node addresses) are stored as an
/// AES-GCM ciphertext under Application Support. The 256-bit key lives only in
/// the Keychain (AfterFirstUnlock) — plaintext never sits in UserDefaults.
enum ProfileVault {
    private static let service = "openflux.profile-vault"
    private static let account = "aes-key-v1"
    private static let fileName = "profiles.v1.enc"
    /// Legacy plaintext keys — migrated once, then removed.
    private static let legacyProfilesKey = "profiles.v1"
    private static let legacySelectedKey = "profiles.selected.v1"
    private static let selectedFileName = "profiles.selected.v1"

    // MARK: - Public API

    /// Loads the profile list. Migrates plaintext UserDefaults on first run.
    static func loadProfiles() -> [Profile] {
        migrateLegacyIfNeeded()
        guard let cipher = readFile(fileName),
              let plain = decrypt(cipher),
              let list = try? JSONDecoder().decode([Profile].self, from: plain)
        else { return [] }
        return list
    }

    static func saveProfiles(_ profiles: [Profile]) throws {
        let plain = try JSONEncoder().encode(profiles)
        let cipher = try encrypt(plain)
        try writeFile(fileName, data: cipher)
        // Drop legacy plaintext if any remains.
        UserDefaults.standard.removeObject(forKey: legacyProfilesKey)
    }

    static func loadSelectedID() -> UUID? {
        migrateLegacyIfNeeded()
        if let data = readFile(selectedFileName),
           let s = String(data: data, encoding: .utf8),
           let id = UUID(uuidString: s) {
            return id
        }
        if let s = UserDefaults.standard.string(forKey: legacySelectedKey),
           let id = UUID(uuidString: s) {
            return id
        }
        return nil
    }

    static func saveSelectedID(_ id: UUID?) {
        if let id, let data = id.uuidString.data(using: .utf8) {
            try? writeFile(selectedFileName, data: data)
        } else {
            try? deleteFile(selectedFileName)
        }
        UserDefaults.standard.removeObject(forKey: legacySelectedKey)
    }

    // MARK: - Migration

    private static func migrateLegacyIfNeeded() {
        guard UserDefaults.standard.data(forKey: legacyProfilesKey) != nil else { return }
        // Already have ciphertext — still wipe plaintext leftovers.
        if readFile(fileName) != nil {
            UserDefaults.standard.removeObject(forKey: legacyProfilesKey)
            return
        }
        guard let data = UserDefaults.standard.data(forKey: legacyProfilesKey),
              let list = try? JSONDecoder().decode([Profile].self, from: data)
        else {
            UserDefaults.standard.removeObject(forKey: legacyProfilesKey)
            return
        }
        try? saveProfiles(list)
        if let s = UserDefaults.standard.string(forKey: legacySelectedKey),
           let id = UUID(uuidString: s) {
            saveSelectedID(id)
        }
        UserDefaults.standard.removeObject(forKey: legacyProfilesKey)
        UserDefaults.standard.removeObject(forKey: legacySelectedKey)
    }

    // MARK: - Crypto

    private static func encrypt(_ plain: Data) throws -> Data {
        let key = try vaultKey()
        let sealed = try AES.GCM.seal(plain, using: key)
        guard let combined = sealed.combined else {
            throw VaultError.sealFailed
        }
        return combined
    }

    private static func decrypt(_ combined: Data) -> Data? {
        guard let key = try? vaultKey() else { return nil }
        guard let box = try? AES.GCM.SealedBox(combined: combined),
              let plain = try? AES.GCM.open(box, using: key)
        else { return nil }
        return plain
    }

    private static func vaultKey() throws -> SymmetricKey {
        if let existing = loadKeyFromKeychain() {
            return SymmetricKey(data: existing)
        }
        var bytes = [UInt8](repeating: 0, count: 32)
        let status = SecRandomCopyBytes(kSecRandomDefault, bytes.count, &bytes)
        guard status == errSecSuccess else { throw VaultError.keygenFailed }
        let data = Data(bytes)
        try saveKeyToKeychain(data)
        return SymmetricKey(data: data)
    }

    private static func loadKeyFromKeychain() -> Data? {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        // Do not require access group if the entitlement string is awkward;
        // Keychain still isolates by app id.
        var out: CFTypeRef?
        let st = SecItemCopyMatching(q as CFDictionary, &out)
        guard st == errSecSuccess, let data = out as? Data, data.count == 32 else { return nil }
        return data
    }

    private static func saveKeyToKeychain(_ data: Data) throws {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account,
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
        // Replace if present.
        SecItemDelete(q as CFDictionary)
        let st = SecItemAdd(q as CFDictionary, nil)
        guard st == errSecSuccess else { throw VaultError.keychainWrite(st) }
    }

    // MARK: - Files

    private static var directory: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        let dir = base.appendingPathComponent("OpenFlux", isDirectory: true)
        if !FileManager.default.fileExists(atPath: dir.path) {
            try? FileManager.default.createDirectory(at: dir, withIntermediateDirectories: true)
            // Exclude from iCloud backup — secrets stay on-device.
            var values = URLResourceValues()
            values.isExcludedFromBackup = true
            var mutable = dir
            try? mutable.setResourceValues(values)
        }
        return dir
    }

    private static func readFile(_ name: String) -> Data? {
        try? Data(contentsOf: directory.appendingPathComponent(name))
    }

    private static func writeFile(_ name: String, data: Data) throws {
        try data.write(to: directory.appendingPathComponent(name), options: [.atomic, .completeFileProtection])
    }

    private static func deleteFile(_ name: String) throws {
        let url = directory.appendingPathComponent(name)
        if FileManager.default.fileExists(atPath: url.path) {
            try FileManager.default.removeItem(at: url)
        }
    }

    enum VaultError: Error {
        case sealFailed
        case keygenFailed
        case keychainWrite(OSStatus)
    }
}
