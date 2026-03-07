using UnityEngine;

public class PlayerController : MonoBehaviour
{
    // 在 Inspector 面板中可以调整的移动速度
    [Header("移动速度")]
    [Tooltip("控制角色在场景中的移动速率")]
    [Range(1f, 2000f)] // 使用 Range 让速度值在 Inspector 中以滑动条形式出现，方便调节
    public float moveSpeed = 100f;

    void Start()
    {
    }

    void Update()
    {
        // --- 1. 获取输入 ---
        // Input.GetAxis("Horizontal") 会获取 A/D 键或左/右方向键的输入
        // 按 A 是 -1，按 D 是 1，不按是 0
        float horizontalInput = Input.GetAxis("Horizontal");

        // Input.GetAxis("Vertical") 会获取 W/S 键或上/下方向键的输入
        // 按 S 是 -1，按 W 是 1，不按是 0
        float verticalInput = Input.GetAxis("Vertical");

        // --- 2. 计算移动方向 ---
        // 创建一个三维向量来表示移动方向
        // 我们希望角色在 X (左右) 和 Z (前后) 平面上移动
        Vector3 moveDirection = new Vector3(horizontalInput, 0f, verticalInput);

        // 【备用方式】直接使用 Transform.Translate()
        // 这个方法会忽略物理碰撞，可能会导致角色穿墙
        transform.Translate(moveDirection * moveSpeed * Time.deltaTime, Space.World);

    }
}