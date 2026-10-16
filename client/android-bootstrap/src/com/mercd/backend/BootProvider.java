package com.mercd.backend;

import android.content.ContentProvider;
import android.content.ContentValues;
import android.content.Context;
import android.content.res.AssetManager;
import android.database.Cursor;
import android.net.Uri;
import android.util.Log;

import java.io.File;
import java.io.FileOutputStream;
import java.io.InputStream;

import mobile.Mobile;

// BootProvider 在 Application.onCreate 之前由系统自动实例化：
// 把 APK assets/backend/* 解压到 filesDir，然后 Mobile.start 拉起进程内
// Go 服务（127.0.0.1:18091），游戏客户端经 gsp.txt 直连，无需任何外部服务。
public class BootProvider extends ContentProvider {
    private static final String TAG = "mercd-boot";
    private static final int PORT = 18091;

    @Override
    public boolean onCreate() {
        final Context ctx = getContext();
        new Thread(new Runnable() {
            public void run() { start(ctx); }
        }, "mercd-boot").start();
        return true;
    }

    private static void start(Context ctx) {
        try {
            File files = ctx.getFilesDir();
            // 解压只发生在安装/升级后首次启动：以 APK lastUpdateTime 为戳。
            // 缺此判断时每次进游戏都重写 ~2.3G assets，启动拖慢约 1-2 分钟。
            long stamp = ctx.getPackageManager()
                    .getPackageInfo(ctx.getPackageName(), 0).lastUpdateTime;
            File marker = new File(files, "state/.backend_extracted");
            if (!(marker.exists() && readStamp(marker) == stamp)) {
                AssetManager am = ctx.getAssets();
                copyTree(am, "backend/cn350", new File(files, "assets/cn350"));
                copyTree(am, "backend/cards", new File(files, "assets/shared/cards"));
                // 单 APK 整包：remote_assets 已并入 assets/unity 走 StreamingAssets，
                // 不再下发解压；清掉旧版本解压残留（~1.1G）与版本状态。
                deleteTree(new File(files, "state/remote_assets"));
                new File(files, "state/remote_assets_state.json").delete();
                marker.getParentFile().mkdirs();
                FileOutputStream out = new FileOutputStream(marker);
                out.write(String.valueOf(stamp).getBytes("UTF-8"));
                out.close();
                Log.i(TAG, "backend assets extracted to " + files);
            } else {
                Log.i(TAG, "backend assets already extracted, skip");
            }

            File state = new File(files, "state");
            //noinspection ResultOfMethodCallIgnored
            state.mkdirs();
            Mobile.start(state.getAbsolutePath(),
                    new File(files, "assets/cn350").getAbsolutePath(),
                    new File(files, "assets/shared/cards").getAbsolutePath(),
                    PORT);
            Log.i(TAG, "Mobile.start returned, status=" + Mobile.status());
        } catch (Throwable t) {
            Log.e(TAG, "backend bootstrap failed", t);
        }
    }

    private static long readStamp(File f) {
        try {
            java.io.FileInputStream in = new java.io.FileInputStream(f);
            byte[] buf = new byte[32];
            int n = in.read(buf);
            in.close();
            return Long.parseLong(new String(buf, 0, Math.max(n, 0), "UTF-8").trim());
        } catch (Throwable t) {
            return -1;
        }
    }

    private static void deleteTree(File f) {
        if (!f.exists()) return;
        File[] children = f.listFiles();
        if (children != null) {
            for (File c : children) deleteTree(c);
        }
        //noinspection ResultOfMethodCallIgnored
        f.delete();
    }

    private static void copyTree(AssetManager am, String src, File dst) throws Exception {
        String[] children = am.list(src);
        if (children == null || children.length == 0) {
            File parent = dst.getParentFile();
            if (parent != null) parent.mkdirs();
            InputStream in = am.open(src);
            FileOutputStream out = new FileOutputStream(dst);
            byte[] buf = new byte[65536];
            int n;
            while ((n = in.read(buf)) > 0) out.write(buf, 0, n);
            out.close();
            in.close();
            return;
        }
        //noinspection ResultOfMethodCallIgnored
        dst.mkdirs();
        for (String child : children) {
            copyTree(am, src + "/" + child, new File(dst, child));
        }
    }

    @Override public Cursor query(Uri uri, String[] p, String s, String[] a, String o) { return null; }
    @Override public String getType(Uri uri) { return null; }
    @Override public Uri insert(Uri uri, ContentValues v) { return null; }
    @Override public int delete(Uri uri, String s, String[] a) { return 0; }
    @Override public int update(Uri uri, ContentValues v, String s, String[] a) { return 0; }
}
