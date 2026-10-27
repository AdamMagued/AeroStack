import SwiftUI
import AppKit

// MARK: - Toast Model

public enum ToastType: Equatable {
    case info
    case success
    case warning
    case error

    public var systemImage: String {
        switch self {
        case .info: return "info.circle.fill"
        case .success: return "checkmark.circle.fill"
        case .warning: return "exclamationmark.triangle.fill"
        case .error: return "xmark.octagon.fill"
        }
    }

    public var color: Color {
        switch self {
        case .info: return .blue
        case .success: return .green
        case .warning: return .orange
        case .error: return .red
        }
    }
}

public struct ToastItem: Identifiable, Equatable {
    public let id: UUID
    public let title: String
    public let subtitle: String?
    public let systemImage: String
    public let type: ToastType
    public let timestamp: Date

    public init(
        id: UUID = UUID(),
        title: String,
        subtitle: String? = nil,
        systemImage: String? = nil,
        type: ToastType = .info
    ) {
        self.id = id
        self.title = title
        self.subtitle = subtitle
        self.type = type
        self.systemImage = systemImage ?? type.systemImage
        self.timestamp = Date()
    }
}

// MARK: - Toast View (Sonner-style floating status pill)

public struct ToastView: View {
    public let toast: ToastItem
    public let onDismiss: () -> Void

    public var body: some View {
        HStack(spacing: EmilSpacing.sm) {
            Image(systemName: toast.systemImage)
                .font(.system(size: 14, weight: .semibold))
                .foregroundColor(toast.type.color)
                .shadow(color: toast.type.color.opacity(0.4), radius: 3, x: 0, y: 0)

            VStack(alignment: .leading, spacing: 1) {
                Text(toast.title)
                    .font(.subheadline)
                    .fontWeight(.medium)
                    .foregroundColor(.primary)

                if let sub = toast.subtitle, !sub.isEmpty {
                    Text(sub)
                        .font(.caption)
                        .foregroundColor(.secondary)
                }
            }

            Button(action: onDismiss) {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(EmilActionIconButtonStyle(tint: .secondary))
            .frame(width: 20, height: 20)
            .padding(.leading, EmilSpacing.xs)
        }
        .padding(.horizontal, EmilSpacing.md)
        .padding(.vertical, EmilSpacing.xs + 2)
        .background(
            Capsule(style: .continuous)
                .fill(.ultraThinMaterial)
        )
        .overlay(
            Capsule(style: .continuous)
                .stroke(Color.primary.opacity(0.10), lineWidth: 1)
        )
        .shadow(color: Color.black.opacity(0.12), radius: 10, x: 0, y: 4)
        .shadow(color: Color.black.opacity(0.04), radius: 2, x: 0, y: 1)
    }
}
