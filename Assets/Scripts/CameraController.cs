// CameraController.cs
using UnityEngine;

public class CameraController : MonoBehaviour
{
    // 公共变量，我们可以在 Unity 编辑器里设置它们
    public Transform target; // 要跟随的目标 (我们的玩家)
    public Vector3 offset = new Vector3(0f, 5f, -10f); // 摄像机相对于目标的偏移量 (默认在玩家后上方)
    public float smoothSpeed = 10f; // 跟随的平滑速度，值越大，跟得越紧

    // LateUpdate 在所有 Update 方法执行完毕后调用。
    // 这很适合用于摄像机逻辑，可以确保玩家已经完成了当帧的移动。
    void LateUpdate()
    {
        // 如果没有设置目标，就什么也不做，防止报错
        if (target == null)
        {
            return;
        }

        // 1. 计算出摄像机应该在的理想位置
        Vector3 desiredPosition = target.position + offset;

        // 2. 使用 Lerp (线性插值) 平滑地从当前位置移动到理想位置
        Vector3 smoothedPosition = Vector3.Lerp(transform.position, desiredPosition, smoothSpeed * Time.deltaTime);

        // 3. 更新摄像机自己的位置
        transform.position = smoothedPosition;

        // 4. (可选但推荐) 让摄像机始终朝向目标
        transform.LookAt(target);
    }
}