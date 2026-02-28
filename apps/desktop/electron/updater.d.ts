export type UpdateStatus = "idle" | "checking" | "available" | "downloading" | "downloaded" | "up_to_date" | "error" | "disabled";
export interface UpdateStateSnapshot {
    status: UpdateStatus;
    currentVersion: string;
    latestVersion: string | null;
    progressPercent: number | null;
    lastCheckedAt: string | null;
    message: string | null;
}
export interface UpdateActionResult {
    success: boolean;
    message: string;
}
declare class DesktopUpdaterService {
    private state;
    private eventBound;
    private ipcBound;
    private checking;
    getState(): UpdateStateSnapshot;
    registerIpc(): void;
    start(): void;
    private updateState;
    private broadcastState;
    checkForUpdates(trigger: "startup" | "manual"): Promise<UpdateActionResult>;
    quitAndInstall(): UpdateActionResult;
}
export declare const desktopUpdater: DesktopUpdaterService;
export {};
