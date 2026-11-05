// Notifications waiting for a click. A notification nothing refers to can be
// collected, and its click is lost with it.
const shown = new Set<Notification>();

/** Brings Loop to the front on channelId: through the app when there is one,
 *  which can raise a window in the background, else in the browser tab. */
function openChannel(channelId: string) {
  if (window.loopAPI?.openChannel) {
    window.loopAPI.openChannel(channelId);
    return;
  }
  window.location.hash = channelId;
  window.focus();
}

/** Shows a system notification that opens channelId when clicked. */
export function notifyChannel(title: string, body: string, channelId: string) {
  const notification = new Notification(title, { body });
  shown.add(notification);
  notification.onclose = () => shown.delete(notification);
  notification.onclick = () => {
    shown.delete(notification);
    openChannel(channelId);
  };
}
