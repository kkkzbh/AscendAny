import { MessageList } from "./MessageList";
import { ChatInput } from "./ChatInput";

export function ChatPanel() {
  return (
    <section className="flex h-full w-full flex-col">
      <MessageList />
      <ChatInput />
    </section>
  );
}
