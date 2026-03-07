// 在Unity材质的Shader下拉菜单中显示的路径。放在"UI/"子菜单下，方便管理和查找。
Shader "UI/SpriteWobbleShader"
{
    // ------------------------------------------------------------------
    // 1. 属性 (Properties)
    // ------------------------------------------------------------------
    Properties
    {
        // _MainTex 是主纹理。对于UI Image组件，它会自动将"Source Image"传递给这个属性。
        _MainTex ("Sprite Texture", 2D) = "white" {}
        
        // _Color 用于给Image染色，它会和Image组件自身的Color属性相乘，提供额外的调色能力。
        _Color ("Tint Color", Color) = (1,1,1,1)
        
        // _WobbleSpeed 控制晃动动画的速度，Range(0, 20)使其在Inspector中显示为滑块，便于调节。
        _WobbleSpeed ("Wobble Speed", Range(0, 20)) = 5
        
        // _WobbleAmount 控制晃动动画的幅度。
        _WobbleAmount ("Wobble Amount", Range(0, 0.1)) = 0.02

        // --- 以下是UI Shader与Unity的Mask组件正确协作所需的标准属性 ---
        // 这些是固定写法，用于接收来自Canvas系统的模板缓冲区(Stencil Buffer)设置。
        _StencilComp ("Stencil Comparison", Float) = 8
        _Stencil ("Stencil ID", Float) = 0
        _StencilOp ("Stencil Operation", Float) = 0
        _StencilWriteMask ("Stencil Write Mask", Float) = 255
        _StencilReadMask ("Stencil Read Mask", Float) = 255
        _ColorMask ("Color Mask", Float) = 15
    }

    // ------------------------------------------------------------------
    // 2. 子着色器 (SubShader)
    // ------------------------------------------------------------------
    SubShader
    {
        // -- 渲染设置 Tags --
        // Tags 必须正确设置，以符合UI系统的渲染规则。
        Tags
        {
            "Queue" = "Transparent"                 // 确保UI元素在所有不透明物体之后渲染。
            "RenderType" = "Transparent"            // 将此Shader标记为透明渲染类型。
            "IgnoreProjector" = "True"              // UI元素通常不受投影器影响。
            "PreviewType" = "Plane"                 // 在材质预览窗口中将材质显示在一个平面上。
            "CanUseSpriteAtlas" = "True"            // 关键！允许Shader正确处理由Unity自动打包的UI图集。
        }

        // --- Stencil (模板) 设置块 ---
        // 这个代码块是让UI Mask组件(圆形、自定义形状遮罩)正常工作的关键！
        // 它会读取上面Properties里的_Stencil相关值，来决定像素是否应该被遮罩剔除。
        Stencil
        {
            Ref [_Stencil]
            Comp [_StencilComp]
            Pass [_StencilOp]
            ReadMask [_StencilReadMask]
            WriteMask [_StencilWriteMask]
        }

        Pass
        {
            // -- GPU渲染状态设置 --
            Blend SrcAlpha OneMinusSrcAlpha          // 经典的透明混合模式，使Alpha通道生效。
            Cull Off                                // 关闭背面剔除，因为UI元素是2D平面。
            Lighting Off                            // 不受场景光照影响。
            ZWrite Off                              // 不写入深度缓冲区，以正确处理透明物体的层叠。

            // -- CG/HLSL代码块开始 --
            CGPROGRAM
            #pragma vertex vert                     // 声明顶点着色器函数的名字是 "vert"。
            #pragma fragment frag                   // 声明片元着色器函数的名字是 "frag"。
            
            #include "UnityUI.cginc"                // 关键！必须包含Unity的UI标准库文件，它提供了UI渲染所需的函数和变量。

            // ------------------------------------------------------------------
            // 3. 数据结构体 (struct)
            // ------------------------------------------------------------------
            
            // a. appdata_t: 从CanvasRenderer传给顶点着色器的数据
            struct appdata_t
            {
                float4 vertex   : POSITION;     // 顶点的本地坐标
                float2 texcoord : TEXCOORD0;    // UV坐标
                fixed4 color    : COLOR;        // Image组件上设置的颜色
            };

            // b. v2f: 从顶点着色器传给片元着色器的数据 "包裹"
            struct v2f
            {
                float4 vertex   : SV_POSITION;  // 顶点在屏幕上的最终位置
                float2 uv       : TEXCOORD0;    // UV坐标
                fixed4 color    : COLOR;        // 混合后的顶点颜色
                // 新增变量：传递顶点的世界坐标
                // 这是为了让片元着色器能正确处理RectMask2D组件的裁剪。
                // TEXCOORD1表示我们借用第二套UV坐标通道来传递这个四维向量数据。
                float4 worldPosition : TEXCOORD1;
            };
            
            // ------------------------------------------------------------------
            // 4. 变量声明
            // ------------------------------------------------------------------
            sampler2D _MainTex;
            fixed4 _Color;
            float _WobbleSpeed;
            float _WobbleAmount;
            // 这个变量由UnityUI.cginc提供，用于接收来自RectMask2D组件的裁剪矩形范围。
            float4 _ClipRect;

            // ------------------------------------------------------------------
            // 5. 顶点着色器 (Vertex Shader)
            // ------------------------------------------------------------------
            v2f vert(appdata_t v)
            {
                v2f o;
                // --- 正确计算世界坐标 ---
                // unity_ObjectToWorld 是Unity内置的、将模型本地坐标转换到世界坐标的矩阵。
                // mul() 是矩阵乘法函数。这行代码计算出顶点的世界坐标。
                o.worldPosition = mul(unity_ObjectToWorld, v.vertex);
                
                // 将顶点位置从本地空间转换到屏幕裁剪空间，决定它最终画在哪里。
                o.vertex = UnityObjectToClipPos(v.vertex);
                
                // 直接传递UV坐标。
                o.uv = v.texcoord;
                
                // 将Image组件的颜色与我们在材质上设置的Tint颜色预先相乘。
                // 在顶点着色器中做这个乘法是一种优化，因为它只对每个顶点（通常是4个）执行一次，
                // 而不是在片元着色器中对每个像素都执行一次。
                o.color = v.color * _Color;
                return o;
            }

            // ------------------------------------------------------------------
            // 6. 片元着色器 (Fragment/Pixel Shader)
            // ------------------------------------------------------------------
            fixed4 frag(v2f i) : SV_Target
            {
                float2 uv = i.uv;

                // --- 核心扰动计算 (完全保留，与Sprite版本一模一样！) ---
                float sinWave = sin(_Time.y * _WobbleSpeed + uv.y * 20.0);
                float offset = sinWave * _WobbleAmount;
                uv.x += offset;
                
                // 使用被修改过的(uv)坐标去采样纹理颜色，产生扭曲效果。
                fixed4 texColor = tex2D(_MainTex, uv);
                
                // 将采样到的纹理颜色与从顶点着色器传来的、已经混合好的颜色相乘。
                texColor *= i.color;

                // --- 正确的UI裁剪逻辑 ---
                // step(a, b) 是一个阶跃函数，如果 b >= a 则返回1，否则返回0。
                // 下面四句连乘，只有当像素的世界坐标(i.worldPosition)的x和y值
                // 同时在_ClipRect定义的矩形范围(x,y,z,w -> left, bottom, right, top)内时，结果才为1。
                float clipFactor = step(_ClipRect.x, i.worldPosition.x) * step(i.worldPosition.x, _ClipRect.z) * step(_ClipRect.y, i.worldPosition.y) * step(i.worldPosition.y, _ClipRect.w);

                // 将颜色的alpha通道乘以这个裁剪因子。
                // 如果像素在裁剪矩形外(clipFactor=0)，它的alpha将变为0，从而变得完全透明，实现裁剪。
                texColor.a *= clipFactor;
                
                // 返回最终计算出的像素颜色，GPU会把它画到屏幕上。
                return texColor;
            }
            ENDCG
        }
    }
}