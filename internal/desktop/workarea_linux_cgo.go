//go:build desktop && linux && cgo && (production || dev)

package desktop

/*
#cgo linux pkg-config: gtk+-3.0
#cgo webkit2_41 pkg-config: webkit2gtk-4.1
#cgo !webkit2_41 pkg-config: webkit2gtk-4.0

#include <gtk/gtk.h>
#include <gdk/gdk.h>
#include <glib.h>

typedef struct {
    gboolean done;
    gboolean ok;
    gboolean used_window_monitor;
    int x;
    int y;
    int width;
    int height;
    int mon_x;
    int mon_y;
    int frame_w;
    int frame_h;
    GMutex mutex;
    GCond cond;
} dm_workarea_state;

static gboolean dm_query_workarea_cb(gpointer data) {
    dm_workarea_state *st = (dm_workarea_state *)data;
    GdkDisplay *display = gdk_display_get_default();
    if (display != NULL) {
        GdkMonitor *mon = NULL;
        GList *toplevels = gtk_window_list_toplevels();
        if (toplevels != NULL) {
            GtkWidget *toplevel = GTK_WIDGET(toplevels->data);
            GdkWindow *win = gtk_widget_get_window(toplevel);
            if (win != NULL) {
                // 仅当窗口已 realize（有 GdkWindow）才声称使用实际窗口显示器。
                mon = gdk_display_get_monitor_at_window(display, win);
                // 真实读取窗口外框相对客户区的差额：宽差=左右边框合计，高差=标题栏+底部边框合计。
                GdkRectangle frame;
                gdk_window_get_frame_extents(win, &frame);
                int cw = 0, ch = 0;
                gtk_window_get_size(GTK_WINDOW(toplevel), &cw, &ch);
                int dw = frame.width - cw;
                int dh = frame.height - ch;
                st->frame_w = dw > 0 ? dw : 0;
                st->frame_h = dh > 0 ? dh : 0;
                st->used_window_monitor = TRUE;
            }
        }
        if (mon == NULL) {
            // startHidden 未 realize 时取不到 GdkWindow：如实回退主显示器，不假称实际窗口显示器。
            mon = gdk_display_get_monitor(display, 0);
        }
        if (mon != NULL) {
            GdkRectangle rect;
            gdk_monitor_get_workarea(mon, &rect);
            // 同一 monitor、同一主线程同时读取 geometry 原点：Wails Linux SetPosition
            // 以 geometry 原点为基准，而工作区原点在有面板时与其不同。
            GdkRectangle geom;
            gdk_monitor_get_geometry(mon, &geom);
            if (rect.width > 0 && rect.height > 0) {
                st->x = rect.x;
                st->y = rect.y;
                st->width = rect.width;
                st->height = rect.height;
                st->mon_x = geom.x;
                st->mon_y = geom.y;
                st->ok = TRUE;
            }
        }
    }
    g_mutex_lock(&st->mutex);
    st->done = TRUE;
    g_cond_signal(&st->cond);
    g_mutex_unlock(&st->mutex);
    return G_SOURCE_REMOVE;
}

static void dm_query_workarea_blocking(dm_workarea_state *st) {
    g_mutex_init(&st->mutex);
    g_cond_init(&st->cond);
    g_mutex_lock(&st->mutex);
    g_idle_add(dm_query_workarea_cb, st);
    while (!st->done) {
        g_cond_wait(&st->cond, &st->mutex);
    }
    g_mutex_unlock(&st->mutex);
    g_mutex_clear(&st->mutex);
    g_cond_clear(&st->cond);
}
*/
import "C"

import "runtime"

// platformWorkArea 在 GTK 主线程读取包含坐标 (x,y) 的显示器工作区矩形（GDK 逻辑像素），
// 并扣除真实窗口装饰（宽/高各自对应轴）使外框适配；同一 monitor 同时读取 geometry 原点
// 存入 MonitorX/MonitorY（供 LinuxSetPos 换算相对偏移）。GDK 非线程安全，
// 故经 g_idle_add 调度到 GTK 主循环并由 C 侧 GMutex/GCond 阻塞等待。
// 需 CGo 与 GTK3/WebKitGTK 4.1 开发库；startHidden 未 realize 时回退主显示器（如实）。
func platformWorkArea(x, y int) (WorkAreaRect, bool) {
	_ = x
	_ = y
	var st C.dm_workarea_state
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	C.dm_query_workarea_blocking(&st)
	runtime.KeepAlive(&st)
	if st.ok != C.gboolean(1) {
		return WorkAreaRect{}, false
	}
	// 工作区为逻辑像素；按各轴扣除真实装饰：宽减左右边框，高减标题栏/底边框。
	w := int(st.width) - int(st.frame_w)
	h := int(st.height) - int(st.frame_h)
	if w <= 0 || h <= 0 {
		return WorkAreaRect{}, false
	}
	return WorkAreaRect{X: int(st.x), Y: int(st.y), Width: w, Height: h, MonitorX: int(st.mon_x), MonitorY: int(st.mon_y)}, true
}
