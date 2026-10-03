import Foundation

/// Локальное хранилище секретов профилей в общем App Group контейнере.
/// Значения доступны приложению и Packet Tunnel Extension и не попадают в
/// providerConfiguration VPN-профиля.
enum Secrets {
    static let appGroup = "group.com.p1neapplexpress-saharev.openflux"
    private static let local = UserDefaults(suiteName: appGroup) ?? .standard
    private static let encryptionPrefix = "openflux.encryption."
    private static let directPrefix = "openflux.direct."
    private static let captchaKey = "openflux.captcha.cookies"

    static func captchaCookies() -> String? { local.string(forKey: captchaKey) }

    @discardableResult
    static func setCaptchaCookies(_ header: String) -> Bool {
        let value = header.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.isEmpty { local.removeObject(forKey: captchaKey) }
        else { local.set(value, forKey: captchaKey) }
        return local.synchronize()
    }

    static func encryptionKey(for profileID: UUID) -> String? {
        local.string(forKey: encryptionPrefix + profileID.uuidString)
    }

    static func hasEncryptionKey(for profileID: UUID) -> Bool {
        encryptionKey(for: profileID) != nil
    }

    @discardableResult
    static func setEncryptionKey(_ secret: String, for profileID: UUID) -> Bool {
        let key = encryptionPrefix + profileID.uuidString
        let value = secret.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.isEmpty { local.removeObject(forKey: key) }
        else { local.set(value, forKey: key) }
        return local.synchronize()
    }

    @discardableResult
    static func removeEncryptionKey(for profileID: UUID) -> Bool {
        local.removeObject(forKey: encryptionPrefix + profileID.uuidString)
        return local.synchronize()
    }

    static func directKey(for profileID: UUID) -> String? {
        local.string(forKey: directPrefix + profileID.uuidString)
    }

    @discardableResult
    static func setDirectKey(_ secret: String, for profileID: UUID) -> Bool {
        let key = directPrefix + profileID.uuidString
        let value = secret.trimmingCharacters(in: .whitespacesAndNewlines)
        if value.isEmpty { local.removeObject(forKey: key) }
        else { local.set(value, forKey: key) }
        return local.synchronize()
    }
}
