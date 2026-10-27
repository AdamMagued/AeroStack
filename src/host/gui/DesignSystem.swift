import SwiftUI
import AppKit

// MARK: - Animation Constants (Emil Kowalski Physics)

public extension Animation {
    /// Snappy spring for immediate pointer interactions and button feedback
    static let emilSnappy = Animation.spring(response: 0.22, dampingFraction: 0.85)

    /// Critically damped spring for layout transitions and structural movement (<250ms feel)
    static let emilCritical = Animation.spring(response: 0.30, dampingFraction: 0.90)

    /// Dynamic entry spring for Sonner-style floating toasts and sheets
    static let emilToast = Animation.spring(response: 0.28, dampingFraction: 0.82)
}

// MARK: - Spacing Scale

public enum EmilSpacing {
    public static let xxs: CGFloat = 4
    public static let xs: CGFloat = 8
    public static let sm: CGFloat = 12
    public static let md: CGFloat = 16
    public static let lg: CGFloat = 20
    public static let xl: CGFloat = 24
}

// MARK: - Button Styles with Pointer Feedback

/// Bouncy button style delivering immediate feedback on pointer-down (scale 0.97)
public struct EmilBouncyButtonStyle: ButtonStyle {
    public var tint: Color? = nil
    public var isDestructive: Bool = false

    public init(tint: Color? = nil, isDestructive: Bool = false) {
        self.tint = tint
        self.isDestructive = isDestructive
    }

    public func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline)
            .fontWeight(.medium)
            .padding(.horizontal, EmilSpacing.sm)
            .padding(.vertical, EmilSpacing.xs)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(backgroundFill(isPressed: configuration.isPressed))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .stroke(borderColor(isPressed: configuration.isPressed), lineWidth: 1)
            )
            .foregroundColor(foregroundColor)
            .scaleEffect(configuration.isPressed ? 0.97 : 1.0)
            .animation(.emilSnappy, value: configuration.isPressed)
    }

    private func backgroundFill(isPressed: Bool) -> Color {
        if let tint = tint {
            return tint.opacity(isPressed ? 0.20 : 0.12)
        }
        if isDestructive {
            return Color.red.opacity(isPressed ? 0.18 : 0.10)
        }
        return Color.primary.opacity(isPressed ? 0.08 : 0.04)
    }

    private func borderColor(isPressed: Bool) -> Color {
        if let tint = tint {
            return tint.opacity(isPressed ? 0.40 : 0.25)
        }
        if isDestructive {
            return Color.red.opacity(isPressed ? 0.35 : 0.20)
        }
        return Color.primary.opacity(isPressed ? 0.15 : 0.08)
    }

    private var foregroundColor: Color {
        if let tint = tint { return tint }
        if isDestructive { return .red }
        return .primary
    }
}

/// Prominent primary button style with Apple Silicon accent and critical damping
public struct EmilProminentButtonStyle: ButtonStyle {
    public var customColor: Color = Color.accentColor

    public init(customColor: Color = Color.accentColor) {
        self.customColor = customColor
    }

    public func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.subheadline)
            .fontWeight(.semibold)
            .padding(.horizontal, EmilSpacing.md)
            .padding(.vertical, EmilSpacing.xs + 1)
            .background(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .fill(customColor.opacity(configuration.isPressed ? 0.88 : 1.0))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 8, style: .continuous)
                    .stroke(Color.white.opacity(0.18), lineWidth: 1)
            )
            .foregroundColor(.white)
            .shadow(color: customColor.opacity(configuration.isPressed ? 0.15 : 0.25), radius: 3, x: 0, y: 1.5)
            .scaleEffect(configuration.isPressed ? 0.97 : 1.0)
            .animation(.emilSnappy, value: configuration.isPressed)
    }
}

/// Compact action icon button with snappy scale (0.92) and subtle hover depth
public struct EmilActionIconButtonStyle: ButtonStyle {
    public var tint: Color
    public var hoverTint: Color?

    public init(tint: Color = .secondary, hoverTint: Color? = nil) {
        self.tint = tint
        self.hoverTint = hoverTint
    }

    public func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.system(size: 13, weight: .semibold))
            .foregroundColor(configuration.isPressed ? (hoverTint ?? tint) : tint)
            .frame(width: 28, height: 28)
            .background(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .fill(configuration.isPressed ? tint.opacity(0.16) : Color.primary.opacity(0.04))
            )
            .overlay(
                RoundedRectangle(cornerRadius: 6, style: .continuous)
                    .stroke(Color.primary.opacity(configuration.isPressed ? 0.15 : 0.06), lineWidth: 1)
            )
            .scaleEffect(configuration.isPressed ? 0.92 : 1.0)
            .animation(.emilSnappy, value: configuration.isPressed)
    }
}

// MARK: - Card Surface ViewModifier

public struct EmilCardModifier: ViewModifier {
    public var isHighlighted: Bool = false
    public var cornerRadius: CGFloat = 12

    public func body(content: Content) -> some View {
        content
            .background(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .fill(Color(NSColor.controlBackgroundColor).opacity(0.65))
            )
            .overlay(
                RoundedRectangle(cornerRadius: cornerRadius, style: .continuous)
                    .stroke(
                        isHighlighted ? Color.accentColor.opacity(0.4) : Color.primary.opacity(0.08),
                        lineWidth: 1
                    )
            )
            .shadow(color: Color.black.opacity(0.03), radius: 4, x: 0, y: 1.5)
    }
}

public extension View {
    func emilCard(isHighlighted: Bool = false, cornerRadius: CGFloat = 12) -> some View {
        self.modifier(EmilCardModifier(isHighlighted: isHighlighted, cornerRadius: cornerRadius))
    }
}

// MARK: - Glowing Status Dot with Subtle Pulse

public struct GlowingStatusDot: View {
    public let color: Color
    public let isPulsing: Bool
    public let size: CGFloat

    @State private var pulseScale: CGFloat = 1.0
    @State private var pulseOpacity: Double = 0.55

    public init(color: Color, isPulsing: Bool = false, size: CGFloat = 8) {
        self.color = color
        self.isPulsing = isPulsing
        self.size = size
    }

    public var body: some View {
        ZStack {
            if isPulsing {
                Circle()
                    .fill(color)
                    .frame(width: size, height: size)
                    .scaleEffect(pulseScale)
                    .opacity(pulseOpacity)
                    .onAppear {
                        withAnimation(
                            Animation.easeInOut(duration: 1.6)
                                .repeatForever(autoreverses: true)
                        ) {
                            pulseScale = 1.85
                            pulseOpacity = 0.0
                        }
                    }
            }

            Circle()
                .fill(color)
                .frame(width: size, height: size)
                .shadow(color: color.opacity(0.65), radius: 3, x: 0, y: 0)
        }
        .frame(width: size * 1.5, height: size * 1.5)
    }
}

// MARK: - Emil Transition Extensions

public extension AnyTransition {
    /// Emil Kowalski recommended enter transition: scales from 0.95 with opacity, never scale 0
    static var emilScaleEnter: AnyTransition {
        .asymmetric(
            insertion: .scale(scale: 0.95).combined(with: .opacity),
            removal: .scale(scale: 0.98).combined(with: .opacity)
        )
    }

    /// Toast entry transition: scales from 0.95 + moves from bottom + fade
    static var emilToastTransition: AnyTransition {
        .asymmetric(
            insertion: .scale(scale: 0.95)
                .combined(with: .opacity)
                .combined(with: .offset(y: 14)),
            removal: .scale(scale: 0.98)
                .combined(with: .opacity)
                .combined(with: .offset(y: 8))
        )
    }
}
