import type { TimelineItem } from "../types";

// TimelineCache keeps the newest items each channel last showed, so a
// channel opened again renders them at once instead of an empty pane while
// its first page loads. Only the newest channels are kept; the oldest one
// goes first.
export class TimelineCache {
  private readonly pages = new Map<string, TimelineItem[]>();

  constructor(
    private readonly maxChannels: number,
    private readonly maxItems: number,
  ) {}

  get(channelId: string): TimelineItem[] | undefined {
    return this.pages.get(channelId);
  }

  // Keeps the newest maxItems of items for channelId, making it the newest
  // channel. An empty list keeps what the channel had.
  set(channelId: string, items: TimelineItem[]): void {
    if (items.length === 0) return;
    this.pages.delete(channelId);
    this.pages.set(channelId, items.slice(-this.maxItems));
    for (const oldest of this.pages.keys()) {
      if (this.pages.size <= this.maxChannels) break;
      this.pages.delete(oldest);
    }
  }
}
