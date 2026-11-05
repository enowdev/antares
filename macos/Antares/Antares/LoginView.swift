import SwiftUI
import AntaresKit

struct LoginView: View {
    @EnvironmentObject private var model: AppModel
    @State private var password = ""
    @State private var newPassword = ""
    @State private var confirmPassword = ""
    @State private var errorMessage: String?
    @State private var isSubmitting = false
    @State private var showsSetPassword = false

    var body: some View {
        VStack(alignment: .leading, spacing: 18) {
            if showsSetPassword {
                setPasswordForm
            } else {
                loginForm
            }
        }
        .padding(28)
        .frame(minWidth: 420, minHeight: 260)
    }

    private var loginForm: some View {
        Form {
            Section {
                SecureField("Password", text: $password)
                    .textContentType(.password)
                    .onSubmit(submitLogin)

                if let errorMessage {
                    Text(errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                }
            } header: {
                Text("Sign in to Antares")
                    .font(.title2)
                    .fontWeight(.semibold)
            } footer: {
                Text("Enter the dashboard password configured for this Antares server.")
            }

            Button(isSubmitting ? "Signing In..." : "Sign In") {
                submitLogin()
            }
            .disabled(password.isEmpty || isSubmitting)
        }
    }

    private var setPasswordForm: some View {
        Form {
            Section {
                SecureField("New Password", text: $newPassword)
                    .textContentType(.newPassword)
                SecureField("Confirm Password", text: $confirmPassword)
                    .textContentType(.newPassword)
                    .onSubmit(submitNewPassword)

                if let errorMessage {
                    Text(errorMessage)
                        .font(.footnote)
                        .foregroundStyle(.red)
                }
            } header: {
                Text("Set a Dashboard Password")
                    .font(.title2)
                    .fontWeight(.semibold)
            } footer: {
                Text("This protects credentials and other sensitive dashboard actions.")
            }

            Button(isSubmitting ? "Saving..." : "Set Password") {
                submitNewPassword()
            }
            .disabled(newPassword.isEmpty || newPassword != confirmPassword || isSubmitting)
        }
    }

    private func submitLogin() {
        guard !password.isEmpty, !isSubmitting else { return }
        isSubmitting = true
        errorMessage = nil

        Task {
            do {
                try await model.login(password: password)
            } catch let error as APIError where error.isUnauthorized {
                errorMessage = "Incorrect password."
            } catch let error as APIError where error.isDashboardPasswordRequired {
                showsSetPassword = true
                errorMessage = nil
            } catch {
                errorMessage = error.localizedDescription
            }
            isSubmitting = false
        }
    }

    private func submitNewPassword() {
        guard !newPassword.isEmpty, newPassword == confirmPassword, !isSubmitting else { return }
        isSubmitting = true
        errorMessage = nil

        Task {
            do {
                try await model.setDashboardPassword(newPassword)
                try await model.login(password: newPassword)
            } catch {
                errorMessage = error.localizedDescription
            }
            isSubmitting = false
        }
    }
}
