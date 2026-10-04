import Foundation

/// File-based replacement for Keychain storage.
///
/// All secrets (encryption keys, captcha cookies, direct keys) are stored in
/// the app's shared group container so both the main app and the tunnel
/// extension can read them — the same access pattern Keychain had, but using
/// plain encrypted files that are only accessible while the device is unlocked
/// or after first unlock (controlled by iOS data protection class).
///
/// Layout inside the group container:
///   secrets/
///     enc-<profileID>.key      — per-profile encryption secret
///     dir-<profileID>.key      — per-profile direct-channel key
///     captcha.cookies          — captcha cookie header (last solved)
///     transport-state.json     — transport stability & masking state
///     profiles-backup.json     — backup copy of profile list
enum FileStorage {

    // MARK: - Group container

    /// App Group that is shared between the app target and the tunnel extension.
    /// Must match the group in both targets' entitlements.
    static let appGroup = "group.com.p1neapplexpress-saharev.openflux"

    static var groupURL: URL? {
        FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: appGroup)
    }

    private static var secretsDir: URL? {
        guard let base = groupURL else { return nil }
        let dir = base.appendingPathComponent("secrets", isDirectory: true)
        try? FileManager.default.createDirectory(at: dir,
                                                  withIntermediateDirectories: true)
        return dir
    }

    // MARK: - Internal helpers

    private static func fileURL(_ name: String) -> URL? {
        secretsDir?.appendingPathComponent(name)
    }

    /// Reads a UTF-8 string from a file. Returns nil if the file is missing or
    /// empty.
    private static func readString(_ name: String) -> String? {
        guard let url = fileURL(name),
              let data = try? Data(contentsOf: url),
              !data.isEmpty,
              let s = String(data: data, encoding: .utf8),
              !s.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
        else { return nil }
        return s.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Writes a UTF-8 string atomically. An empty string deletes the file.
    @discardableResult
    private static func writeString(_ name: String, _ value: String) -> Bool {
        let trimmed = value.trimmingCharacters(in: .whitespacesAndNewlines)
        guard let url = fileURL(name) else { return false }
        if trimmed.isEmpty {
            try? FileManager.default.removeItem(at: url)
            return true
        }
        guard let data = trimmed.data(using: .utf8) else { return false }
        // Atomic write via a temp file + rename.
        let tmp = url.appendingPathExtension("tmp")
        do {
            // kCFURLFileProtectionCompleteUnlessOpen — readable after first
            // unlock, which matches the old Keychain kSecAttrAccessibleAfterFirstUnlock.
            try data.write(to: tmp, options: .atomic)
            try (tmp as NSURL).setResourceValue(
                URLFileProtection.completeUnlessOpen,
                forKey: .fileProtectionKey)
            _ = try FileManager.default.replaceItemAt(url, withItemAt: tmp)
            return true
        } catch {
            try? FileManager.default.removeItem(at: tmp)
            return false
        }
    }

    @discardableResult
    private static func deleteFile(_ name: String) -> Bool {
        guard let url = fileURL(name) else { return false }
        do {
            try FileManager.default.removeItem(at: url)
            return true
        } catch {
            return (error as NSError).code == NSFileNoSuchFileError
        }
    }

    // MARK: - Encryption keys (per-profile)

    static func encryptionKey(for profileID: UUID) -> String? {
        readString("enc-\(profileID.uuidString).key")
    }

    static func hasEncryptionKey(for profileID: UUID) -> Bool {
        encryptionKey(for: profileID) != nil
    }

    @discardableResult
    static func setEncryptionKey(_ secret: String, for profileID: UUID) -> Bool {
        writeString("enc-\(profileID.uuidString).key", secret)
    }

    @discardableResult
    static func removeEncryptionKey(for profileID: UUID) -> Bool {
        deleteFile("enc-\(profileID.uuidString).key")
    }

    // MARK: - Direct-channel keys (per-profile)

    static func directKey(for profileID: UUID) -> String? {
        readString("dir-\(profileID.uuidString).key")
    }

    @discardableResult
    static func setDirectKey(_ secret: String, for profileID: UUID) -> Bool {
        writeString("dir-\(profileID.uuidString).key", secret)
    }

    @discardableResult
    static func removeDirectKey(for profileID: UUID) -> Bool {
        deleteFile("dir-\(profileID.uuidString).key")
    }

    // MARK: - Captcha cookies

    static func captchaCookies() -> String? {
        readString("captcha.cookies")
    }

    @discardableResult
    static func setCaptchaCookies(_ header: String) -> Bool {
        writeString("captcha.cookies", header)
    }

    // MARK: - Transport state (stability & masking metadata)

    static func saveTransportState(_ state: [String: Any]) {
        guard let url = fileURL("transport-state.json") else { return }
        guard let data = try? JSONSerialization.data(withJSONObject: state,
                                                     options: .prettyPrinted) else { return }
        let tmp = url.appendingPathExtension("tmp")
        try? data.write(to: tmp, options: .atomic)
        try? (tmp as NSURL).setResourceValue(URLFileProtection.completeUnlessOpen,
                                             forKey: .fileProtectionKey)
        _ = try? FileManager.default.replaceItemAt(url, withItemAt: tmp)
    }

    static func loadTransportState() -> [String: Any]? {
        guard let url = fileURL("transport-state.json"),
              let data = try? Data(contentsOf: url),
              let obj = try? JSONSerialization.jsonObject(with: data) as? [String: Any]
        else { return nil }
        return obj
    }

    // MARK: - Profile list backup (mirrors ProfileStore for the extension)

    /// Saves a JSON-encoded profile list to the shared container so the tunnel
    /// extension can access profile metadata if needed.
    static func saveProfilesBackup(_ data: Data) {
        guard let url = fileURL("profiles-backup.json") else { return }
        let tmp = url.appendingPathExtension("tmp")
        try? data.write(to: tmp, options: .atomic)
        try? (tmp as NSURL).setResourceValue(URLFileProtection.completeUnlessOpen,
                                             forKey: .fileProtectionKey)
        _ = try? FileManager.default.replaceItemAt(url, withItemAt: tmp)
    }

    static func loadProfilesBackup() -> Data? {
        guard let url = fileURL("profiles-backup.json") else { return nil }
        return try? Data(contentsOf: url)
    }

    // MARK: - Migration from Keychain

    /// One-time migration: copies every value that exists in Keychain into files
    /// and then removes the Keychain items. Safe to call on every launch — the
    /// Keychain items are deleted after being migrated, so subsequent calls are
    /// no-ops.
    static func migrateFromKeychain(profileIDs: [UUID]) {
        // Captcha cookies
        if let v = KeychainLegacy.captchaCookies() {
            setCaptchaCookies(v)
            KeychainLegacy.deleteCaptchaCookies()
        }
        // Per-profile secrets
        for id in profileIDs {
            if let v = KeychainLegacy.encryptionKey(for: id) {
                setEncryptionKey(v, for: id)
                KeychainLegacy.deleteEncryptionKey(for: id)
            }
            if let v = KeychainLegacy.directKey(for: id) {
                setDirectKey(v, for: id)
                KeychainLegacy.deleteDirectKey(for: id)
            }
        }
    }
}

// MARK: - Legacy Keychain reader (migration only, write path removed)

/// Read-only access to the old Keychain items so migrateFromKeychain can move
/// them to files on first launch. No new writes go through this enum.
private enum KeychainLegacy {
    private static let accessGroup = "8GQH8GQ252.com.p1neapplexpress-saharev.openflux"
    private static let service    = "openflux.encryption"
    private static let captchaService = "openflux.captcha"
    private static let captchaAccount = "cookies"
    private static let directKeyService = "openflux.directkey"

    static func captchaCookies() -> String? {
        var q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: captchaService,
            kSecAttrAccount as String: captchaAccount,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data,
              let s = String(data: data, encoding: .utf8) else { return nil }
        return s
    }

    static func deleteCaptchaCookies() {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: captchaService,
            kSecAttrAccount as String: captchaAccount,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(q as CFDictionary)
    }

    static func encryptionKey(for id: UUID) -> String? {
        var q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: id.uuidString,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data,
              let s = String(data: data, encoding: .utf8) else { return nil }
        return s
    }

    static func deleteEncryptionKey(for id: UUID) {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: id.uuidString,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(q as CFDictionary)
    }

    static func directKey(for id: UUID) -> String? {
        var q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: directKeyService,
            kSecAttrAccount as String: id.uuidString,
            kSecAttrAccessGroup as String: accessGroup,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var out: CFTypeRef?
        guard SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess,
              let data = out as? Data,
              let s = String(data: data, encoding: .utf8) else { return nil }
        return s
    }

    static func deleteDirectKey(for id: UUID) {
        let q: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: directKeyService,
            kSecAttrAccount as String: id.uuidString,
            kSecAttrAccessGroup as String: accessGroup,
        ]
        SecItemDelete(q as CFDictionary)
    }
}
