export type SignupPolicy =
  | "username_password_only"
  | "require_phone_or_email"
  | "require_phone_and_email";

export interface AuthPolicy {
  signupPolicy: SignupPolicy;
  requirePhone: boolean;
  requireEmail: boolean;
}

export interface AuthProfile {
  displayName: string | null;
  studentId: string | null;
  ptaNickname: string | null;
}

export type AuthProvisionSource = "local" | "external_sso";

export interface AuthAccount extends AuthProfile {
  accountId: string;
  username: string;
  provisionSource: AuthProvisionSource;
  localPasswordEnabled: boolean;
}

export interface AuthTokens {
  accessToken: string;
  accessTokenExpiresAt: string;
  refreshToken: string;
  refreshTokenExpiresAt: string;
  account: AuthAccount;
}
