import { MessageList } from "./MessageList";
import { ChatInput } from "./ChatInput";

export function ChatPanel() {
  return (
    <div className="flex h-full w-full flex-col">
      <MessageList />
      <ChatInput />
    </div>
  );
}
