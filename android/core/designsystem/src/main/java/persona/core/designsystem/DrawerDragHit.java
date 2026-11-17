package persona.core.designsystem;

import android.view.View;

import androidx.annotation.OptIn;
import androidx.compose.ui.InternalComposeUiApi;
import androidx.compose.ui.Modifier;
import androidx.compose.ui.node.LayoutNode;

/**
 * Compose publishes these on the JVM, and Kotlin's metadata does not expose them as properties
 * ({@code LayoutNode.getInteropView}, {@code getNodes$ui}, {@code Modifier.Node.getChild$ui}).
 */
final class DrawerDragHit {
    private DrawerDragHit() {}

    @OptIn(markerClass = InternalComposeUiApi.class)
    static boolean hasAndroidView(LayoutNode node) {
        View view = node.getInteropView();
        return view != null;
    }

    static Modifier.Node head(LayoutNode node) {
        return node.getNodes$ui().getHead$ui();
    }

    static Modifier.Node next(Modifier.Node node) {
        return node.getChild$ui();
    }
}
