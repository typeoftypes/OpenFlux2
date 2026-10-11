import SwiftUI
import UIKit

/// About screen: project info + support via Telegram.
struct InfoView: View {
    @Environment(\.dismiss) private var dismiss
    @State private var copied: String?

    private let tg = "t.me/typeoftypes"
    private let tgURL = URL(string: "https://t.me/typeoftypes")!

    var body: some View {
        NavigationView {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    VStack(alignment: .leading, spacing: 6) {
                        Text("OpenFlux").font(.title2).bold()
                        Text("TCP-туннель через скрытый транспорт. Клиент поднимает локальный SOCKS5 и системный VPN, трафик идёт через exit-node.")
                            .font(.footnote).foregroundColor(.secondary)
                    }

                    VStack(alignment: .leading, spacing: 12) {
                        Text("Поддержать проект ♥")
                            .font(.headline)
                        Text("Напиши в Telegram — там же можно поддержать разработку.")
                            .font(.caption).foregroundColor(.secondary)

                        Link(destination: tgURL) {
                            HStack {
                                Image(systemName: "paperplane.fill")
                                Text(tg)
                                    .font(.subheadline.bold())
                                Spacer()
                                Image(systemName: "arrow.up.right")
                                    .font(.caption)
                            }
                            .padding(12)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .background(Color(.tertiarySystemBackground))
                            .clipShape(RoundedRectangle(cornerRadius: 8))
                        }
                        .buttonStyle(.plain)

                        Button {
                            UIPasteboard.general.string = tg
                            copied = "Telegram"
                        } label: {
                            Label(copied == "Telegram" ? "Скопировано" : "Скопировать t.me/typeoftypes",
                                  systemImage: copied == "Telegram" ? "checkmark.circle.fill" : "doc.on.doc")
                                .font(.caption)
                        }
                        .buttonStyle(.bordered)

                        if copied != nil {
                            Label("Скопировано в буфер", systemImage: "checkmark.circle.fill")
                                .font(.caption).foregroundColor(.green)
                        }
                    }
                    .padding()
                    .background(Color(.secondarySystemBackground))
                    .clipShape(RoundedRectangle(cornerRadius: 12))

                    Spacer(minLength: 0)
                }
                .padding()
            }
            .navigationTitle("О приложении")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .navigationBarTrailing) {
                    Button("Закрыть") { dismiss() }
                }
            }
        }
        .navigationViewStyle(.stack)
    }
}
